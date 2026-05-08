package fleetsec

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/karamble/diginode-cc/internal/audit"
	"github.com/karamble/diginode-cc/internal/database"
	pb "github.com/karamble/diginode-cc/internal/meshpb"
	"github.com/karamble/diginode-cc/internal/serial"
)

// SerialSender is the subset of *serial.Manager that fleetsec writes to.
// Defined as an interface so tests can stub it without requiring a real
// USB device.
type SerialSender interface {
	SendToRadio(data []byte) error
}

// LocalNodeProvider supplies the locally-attached Heltec's mesh node
// number. Implemented by *meshtastic.Dispatcher; isolated as an
// interface so tests can stub it.
type LocalNodeProvider interface {
	LocalNodeNum() uint32
}

// Service is the public API that handlers in internal/api/ call into.
// Methods grouped by topic:
//
//   - Identity        → identity.go
//   - Trust roster    → trust.go
//   - Channels + PSK  → channels.go (step 8)
//   - Recovery        → recovery.go (step 9)
//
// All mutating operations serialize on Service.adminMu so two operators
// can't fire conflicting admin transactions simultaneously. The lock is
// held only across the single admin round-trip; long-running operations
// (e.g. fleet-wide PSK rotation) take the lock once per target.
type Service struct {
	store      *Store
	tracker    *Tracker
	audit      *audit.Service
	serial     SerialSender
	localNode  LocalNodeProvider

	adminMu sync.Mutex // serializes admin transactions
}

// NewService wires up the storage layer, transaction tracker, audit
// logger, serial sender, and local-node provider. The returned tracker
// must be registered with the dispatcher via SetAdminReplyHandler so
// the service receives Routing acks and AdminMessage replies.
func NewService(db *database.DB, audit *audit.Service, ser SerialSender, local LocalNodeProvider) *Service {
	return &Service{
		store:     NewStore(db),
		tracker:   NewTracker(),
		audit:     audit,
		serial:    ser,
		localNode: local,
	}
}

// Tracker exposes the underlying transaction tracker so the dispatcher
// can wire it via meshtastic.Dispatcher.SetAdminReplyHandler. The tracker
// implements meshtastic.AdminReplyHandler.
func (s *Service) Tracker() *Tracker { return s.tracker }

// --- Errors ---

var (
	// ErrLocalNodeUnknown is returned when the local Heltec's node
	// number isn't yet known. Happens during the brief window between
	// startup and the first NodeInfo from the wantConfig dump.
	ErrLocalNodeUnknown = errors.New("local Heltec node number not yet known; wait for serial connection to settle")

	// ErrSerialNotReady is returned when the serial manager is not yet
	// connected to the Heltec.
	ErrSerialNotReady = errors.New("serial connection to Heltec not ready")

	// ErrLockoutPrevented is returned when an admin_key edit would
	// remove every known operator pubkey from a node's trust list.
	ErrLockoutPrevented = errors.New("operation refused: would remove all known admin pubkeys (use force flag with explicit acknowledgement)")

	// ErrManagedLockdownPrevented is returned when SetIsManaged(true)
	// is called for a node that hasn't been verify-acked recently.
	ErrManagedLockdownPrevented = errors.New("operation refused: cannot enable is_managed without recent successful verify (use force flag with explicit acknowledgement)")

	// ErrInvalidAck is returned when a force-flag operation lacks the
	// required typed acknowledgement string.
	ErrInvalidAck = errors.New("operation requires typed acknowledgement")
)

// --- Helpers used across identity.go and trust.go ---

// runLocalAdmin sends an AdminMessage to the LOCAL Heltec (no PKC) and
// waits for a Routing ack with success. Returns the AdminMessage reply
// payload if the firmware responded with one (e.g. get_*_response), or
// nil if just an ack.
//
// The caller must hold s.adminMu (or an outer lock that excludes other
// admin paths).
func (s *Service) runLocalAdmin(ctx context.Context, msg *pb.AdminMessage, kind string) (*pb.AdminMessage, error) {
	localNum := s.localNode.LocalNodeNum()
	if localNum == 0 {
		return nil, ErrLocalNodeUnknown
	}
	frame, packetID, err := serial.BuildAdminPacket(localNum, msg)
	if err != nil {
		return nil, fmt.Errorf("build local admin packet: %w", err)
	}
	return s.send(ctx, frame, packetID, kind, DefaultLocalAdminTimeout)
}

// runRemoteAdmin sends an AdminMessage to a REMOTE node via PKC. Same
// return shape as runLocalAdmin.
func (s *Service) runRemoteAdmin(ctx context.Context, remoteNodeNum uint32, msg *pb.AdminMessage, kind string) (*pb.AdminMessage, error) {
	if remoteNodeNum == 0 {
		return nil, errors.New("remote node number must be non-zero")
	}
	frame, packetID, err := serial.BuildAdminPacketPKC(remoteNodeNum, msg)
	if err != nil {
		return nil, fmt.Errorf("build remote admin packet: %w", err)
	}
	return s.send(ctx, frame, packetID, kind, DefaultRemoteAdminTimeout)
}

// send is the shared transmit + await loop. Registers the transaction,
// writes the frame, blocks on the reply channel until either an
// AdminMessage reply arrives, a Routing ack arrives, or the timeout/
// caller-cancel fires.
func (s *Service) send(ctx context.Context, frame []byte, packetID uint32, kind string, timeout time.Duration) (*pb.AdminMessage, error) {
	if s.serial == nil {
		return nil, ErrSerialNotReady
	}
	reply, err := s.tracker.Begin(ctx, packetID, kind, timeout)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	if err := s.serial.SendToRadio(frame); err != nil {
		return nil, fmt.Errorf("send to radio: %w", err)
	}

	r, ok := <-reply
	if !ok {
		return nil, errors.New("reply channel closed without delivery")
	}
	switch r.Kind {
	case ReplyTimeout:
		return nil, fmt.Errorf("%s: %w", kind, r.Err)
	case ReplyCancelled:
		return nil, fmt.Errorf("%s: %w", kind, r.Err)
	case ReplyAdminMsg:
		return r.Admin, nil
	case ReplyRoutingAck:
		if !r.Successful() {
			reason := pb.Routing_NONE
			if r.Routing != nil {
				reason = r.Routing.GetErrorReason()
			}
			return nil, fmt.Errorf("%s: routing error %v", kind, reason)
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("%s: unexpected reply kind %v", kind, r.Kind)
	}
}

// extractSecurityConfig pulls SecurityConfig out of a get_config_response
// AdminMessage reply. Returns nil + error if the reply isn't shaped right.
func extractSecurityConfig(reply *pb.AdminMessage) (*pb.Config_SecurityConfig, error) {
	if reply == nil {
		return nil, errors.New("nil admin reply")
	}
	cfg := reply.GetGetConfigResponse()
	if cfg == nil {
		return nil, errors.New("admin reply missing get_config_response")
	}
	sec := cfg.GetSecurity()
	if sec == nil {
		return nil, errors.New("config missing security sub-message")
	}
	return sec, nil
}

// extractChannel pulls a Channel out of a get_channel_response.
func extractChannel(reply *pb.AdminMessage) (*pb.Channel, error) {
	if reply == nil {
		return nil, errors.New("nil admin reply")
	}
	ch := reply.GetGetChannelResponse()
	if ch == nil {
		return nil, errors.New("admin reply missing get_channel_response")
	}
	return ch, nil
}

// auditFleet wraps audit.Service.Log with the fleetsec.* action prefix
// and a redaction helper that drops any field whose key suggests it
// holds secret material. The redaction is a defensive belt-and-braces:
// the call sites should already pass only fingerprints, but this
// catches a buggy caller before secrets land in the audit_log table.
func (s *Service) auditFleet(ctx context.Context, userID, action, resource, resourceID string, details map[string]any) {
	if s.audit == nil {
		slog.Debug("fleetsec: no audit service configured", "action", action)
		return
	}
	clean := redactSecrets(details)
	s.audit.Log(ctx, userID, "fleetsec."+action, resource, resourceID, "", clean)
}

// redactSecrets returns a shallow copy of m with any values under keys
// matching a secret-suggestive name replaced by "<redacted>".
func redactSecrets(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if isSecretKey(k) {
			out[k] = "<redacted>"
			continue
		}
		out[k] = v
	}
	return out
}

func isSecretKey(k string) bool {
	switch k {
	case "psk", "psk_b64", "private_key", "priv_b64", "privkey", "secret":
		return true
	}
	return false
}

// pickKnownFingerprints returns the subset of fingerprints in keyFPs
// that appear in our identity registry as non-revoked. Used by the
// lockout-prevention check: a SetAdminKeys whose result list shares
// at least one entry with the registry's non-revoked set is considered
// safe; otherwise refused unless force-acked.
func (s *Service) pickKnownFingerprints(ctx context.Context, keyFPs []string) ([]string, error) {
	idents, err := s.store.ListIdentities(ctx)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(idents))
	for _, id := range idents {
		if id.Role != IdentityRoleRevoked {
			known[id.Fingerprint] = true
		}
	}
	var match []string
	for _, fp := range keyFPs {
		if known[fp] {
			match = append(match, fp)
		}
	}
	return match, nil
}

// fingerprintsFromAdminKeys hashes each X25519 pubkey in the admin_key
// list to its display fingerprint.
func fingerprintsFromAdminKeys(keys [][]byte) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, Fingerprint(k))
	}
	return out
}

// pubkeysForFingerprints loads pubkey bytes from the registry, in the
// same order as the requested fingerprints. Returns ErrNotFound if any
// fingerprint isn't registered.
func (s *Service) pubkeysForFingerprints(ctx context.Context, fps []string) ([][]byte, error) {
	out := make([][]byte, 0, len(fps))
	for _, fp := range fps {
		rec, err := s.store.GetIdentityByFingerprint(ctx, fp)
		if err != nil {
			return nil, fmt.Errorf("fingerprint %s: %w", fp, err)
		}
		out = append(out, rec.PublicKey)
	}
	return out, nil
}

// proto.Marshal is exposed via the unused-import shield. Kept for
// future use by handlers that need to surface raw payloads.
var _ = proto.Marshal
