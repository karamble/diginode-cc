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
	"github.com/karamble/diginode-cc/internal/fleetsec/jobs"
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
	store     *Store
	tracker   *Tracker
	audit     *audit.Service
	serial    SerialSender
	localNode LocalNodeProvider
	jobs         *jobs.Store           // optional, set via SetJobsStore; required for the new async API path
	recoveryHook *recoveryDispatchHook // optional, set via SetupRecoveryHook; nil disables event-driven detection

	hubRef hubRef // optional WS broadcaster, set via WireHub

	adminMu sync.Mutex // serializes admin transactions

	// sessionPasskeys caches per-remote AdminMessage.session_passkey values.
	// Meshtastic firmware emits a fresh passkey in every get_*_response
	// AdminMessage and REQUIRES it on subsequent set_* admins (300s TTL,
	// per-(local_pubkey, remote_pubkey) pair). Without it remote Set*
	// returns routing-error ADMIN_BAD_SESSION_KEY, even when the sender's
	// admin_key is otherwise authorized. Captured in send() on every
	// admin reply, looked up in runRemoteAdmin before each outbound,
	// invalidated on BAD_SESSION_KEY (lets the next attempt re-establish
	// via a fresh Get).
	sessionPasskeys   map[uint32][]byte
	sessionPasskeysMu sync.Mutex
}

// NewService wires up the storage layer, transaction tracker, audit
// logger, serial sender, and local-node provider. The returned tracker
// must be registered with the dispatcher via SetAdminReplyHandler so
// the service receives Routing acks and AdminMessage replies.
func NewService(db *database.DB, audit *audit.Service, ser SerialSender, local LocalNodeProvider) *Service {
	return &Service{
		store:           NewStore(db),
		tracker:         NewTracker(),
		audit:           audit,
		serial:          ser,
		localNode:       local,
		sessionPasskeys: make(map[uint32][]byte),
	}
}

// cacheSessionPasskey stores a remote-emitted admin session passkey for
// future set_* admin calls. Called from send() whenever an inbound admin
// reply carries a non-empty SessionPasskey. The firmware-side TTL is
// 300s; we don't TTL on our side because BAD_SESSION_KEY on the next Set
// will trigger invalidateSessionPasskey + re-establish on the retry.
func (s *Service) cacheSessionPasskey(nodeNum uint32, passkey []byte) {
	if nodeNum == 0 || len(passkey) == 0 {
		return
	}
	s.sessionPasskeysMu.Lock()
	if s.sessionPasskeys == nil {
		s.sessionPasskeys = make(map[uint32][]byte)
	}
	s.sessionPasskeys[nodeNum] = append([]byte(nil), passkey...)
	s.sessionPasskeysMu.Unlock()
}

// getSessionPasskey returns the cached passkey for nodeNum, or nil.
func (s *Service) getSessionPasskey(nodeNum uint32) []byte {
	s.sessionPasskeysMu.Lock()
	defer s.sessionPasskeysMu.Unlock()
	if pk, ok := s.sessionPasskeys[nodeNum]; ok {
		return append([]byte(nil), pk...)
	}
	return nil
}

// invalidateSessionPasskey drops the cached passkey for nodeNum. Used
// when a remote returns ADMIN_BAD_SESSION_KEY: the cached value has
// either expired or the remote rebooted; the next Get round-trip will
// install a fresh one.
func (s *Service) invalidateSessionPasskey(nodeNum uint32) {
	s.sessionPasskeysMu.Lock()
	delete(s.sessionPasskeys, nodeNum)
	s.sessionPasskeysMu.Unlock()
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
	// expectedFrom = localNum: routing/admin replies for local admin
	// legitimately come from the local Heltec.
	return s.send(ctx, frame, packetID, kind, DefaultLocalAdminTimeout, adminMessageExpectsReply(msg), localNum)
}

// runRemoteAdmin sends an AdminMessage to a REMOTE node via PKC. Same
// return shape as runLocalAdmin. Uses DefaultRemoteAdminTimeout (30s)
// — fast unreachability detection. For high-latency in-transaction
// frames where EU 868 duty cycle throttling can push the actual TX
// 30+ seconds after enqueue, use runRemoteAdminLong instead.
func (s *Service) runRemoteAdmin(ctx context.Context, remoteNodeNum uint32, msg *pb.AdminMessage, kind string) (*pb.AdminMessage, error) {
	return s.runRemoteAdminWithTimeout(ctx, remoteNodeNum, msg, kind, DefaultRemoteAdminTimeout)
}

// runRemoteAdminLong is the duty-cycle-tolerant variant. The 150s
// timeout (LongRemoteAdminTimeout) accounts for the case where the
// local Heltec's TX queue is throttled by the EU 868 1% duty cycle
// (firmware-side dutyCycle=10 percent of hour, polite mode halves to
// 5%) and an admin frame waits ~30s in queue before going on-air. Use
// for the in-transaction frames of begin/commit_edit_settings sequences
// where the Pi has just bursted multiple admin frames.
func (s *Service) runRemoteAdminLong(ctx context.Context, remoteNodeNum uint32, msg *pb.AdminMessage, kind string) (*pb.AdminMessage, error) {
	return s.runRemoteAdminWithTimeout(ctx, remoteNodeNum, msg, kind, LongRemoteAdminTimeout)
}

func (s *Service) runRemoteAdminWithTimeout(ctx context.Context, remoteNodeNum uint32, msg *pb.AdminMessage, kind string, timeout time.Duration) (*pb.AdminMessage, error) {
	if remoteNodeNum == 0 {
		return nil, errors.New("remote node number must be non-zero")
	}
	// Inject the cached admin session passkey for this remote, if any.
	// Required for set_* admin packets; ignored by the firmware on Get*.
	// If we have nothing cached, we send with an empty passkey -- the
	// firmware accepts that as the start of a session for Get*, then
	// includes a fresh passkey in its reply that we'll cache via send().
	// For Set* without a cached passkey, the firmware will reject with
	// ADMIN_BAD_SESSION_KEY; the migration helpers do a GetChannel
	// first to populate the cache before in-transaction frames go out.
	if pk := s.getSessionPasskey(remoteNodeNum); len(pk) > 0 {
		msg.SessionPasskey = pk
	}
	frame, packetID, err := serial.BuildAdminPacketPKC(remoteNodeNum, msg)
	if err != nil {
		return nil, fmt.Errorf("build remote admin packet: %w", err)
	}
	// expectedFrom = remoteNodeNum: only the actual target's reply
	// counts. The Pi-Heltec emits a from=local "transmitted it" loopback
	// routing ack for every outbound packet -- without this filter, that
	// loopback would falsely succeed every PKC Set* even when the remote
	// is unreachable or unpowered.
	return s.send(ctx, frame, packetID, kind, timeout, adminMessageExpectsReply(msg), remoteNodeNum)
}

// fireAndForgetRemoteAdmin queues an admin frame for the local Heltec
// to transmit but does NOT register a transaction or wait for any
// reply. Used for the intermediate frames inside an atomic
// begin/commit transaction (begin_edit_settings + 1+ SetChannel
// frames) where the firmware's reply behaviour is unreliable -- our
// Pi-side hardware testing showed begin_edit_settings over PKC admin
// frequently produces no detectable routing ack at all, leaving any
// blocking wait to time out at 150s+ even though the frame was
// processed correctly. The COMMIT frame is the only one we wait on
// (via runRemoteAdminLong), and its routing ack reflects the success
// of the whole transaction.
//
// This call still applies the cached session_passkey + builds a PKC
// packet -- the firmware-side authentication path is unchanged. It
// just bypasses the controller-side wait.
func (s *Service) fireAndForgetRemoteAdmin(remoteNodeNum uint32, msg *pb.AdminMessage) error {
	if s.serial == nil {
		return ErrSerialNotReady
	}
	if remoteNodeNum == 0 {
		return errors.New("remote node number must be non-zero")
	}
	if pk := s.getSessionPasskey(remoteNodeNum); len(pk) > 0 {
		msg.SessionPasskey = pk
	}
	frame, _, err := serial.BuildAdminPacketPKC(remoteNodeNum, msg)
	if err != nil {
		return fmt.Errorf("build remote admin packet: %w", err)
	}
	return s.serial.SendToRadio(frame)
}

// adminMessageExpectsReply reports whether the firmware will follow up
// the transport-level Routing ack with a get_*_response AdminMessage
// payload. Get*Request variants expect such a reply; Set* and command
// variants do not.
//
// The Tracker uses this to disambiguate: Set messages resolve on the
// Routing ack, Get messages resolve on the AdminMessage (the ack only
// confirms transport). Without the distinction the Routing ack races
// the AdminMessage on Get paths and the AdminMessage gets dropped --
// the symptom is "nil admin reply" 502s on /api/fleet-security/identity
// and the other Get*-backed read endpoints.
func adminMessageExpectsReply(msg *pb.AdminMessage) bool {
	if msg == nil {
		return false
	}
	switch msg.GetPayloadVariant().(type) {
	case *pb.AdminMessage_GetChannelRequest,
		*pb.AdminMessage_GetOwnerRequest,
		*pb.AdminMessage_GetConfigRequest,
		*pb.AdminMessage_GetModuleConfigRequest,
		*pb.AdminMessage_GetCannedMessageModuleMessagesRequest,
		*pb.AdminMessage_GetDeviceMetadataRequest,
		*pb.AdminMessage_GetRingtoneRequest,
		*pb.AdminMessage_GetDeviceConnectionStatusRequest,
		*pb.AdminMessage_GetNodeRemoteHardwarePinsRequest,
		*pb.AdminMessage_GetUiConfigRequest:
		return true
	default:
		return false
	}
}

// send is the shared transmit + await loop. Registers the transaction,
// writes the frame, blocks on the reply channel until either an
// AdminMessage reply arrives, a Routing ack arrives, or the timeout/
// caller-cancel fires.
//
// expectsAdminReply must match the outbound message shape: true for
// Get*Request AdminMessages (firmware follows the Routing ack with a
// get_*_response), false for Set* and command variants. See
// adminMessageExpectsReply.
//
// expectedFrom is the only node-num whose reply should resolve this
// transaction. For runLocalAdmin pass the local node-num; for
// runRemoteAdmin pass the remote target. The loopback "transmitted it"
// routing ack the Pi-Heltec emits on every outbound packet has
// from=local_num and would otherwise falsely succeed remote Set*.
func (s *Service) send(ctx context.Context, frame []byte, packetID uint32, kind string, timeout time.Duration, expectsAdminReply bool, expectedFrom uint32) (*pb.AdminMessage, error) {
	if s.serial == nil {
		return nil, ErrSerialNotReady
	}
	reply, err := s.tracker.Begin(ctx, packetID, kind, timeout, expectsAdminReply, expectedFrom)
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
		// Capture the firmware-emitted session passkey for this remote.
		// Set* admin against this remote later will pick it up via
		// runRemoteAdmin's getSessionPasskey lookup.
		if r.Admin != nil && len(r.Admin.GetSessionPasskey()) > 0 && expectedFrom != 0 {
			s.cacheSessionPasskey(expectedFrom, r.Admin.GetSessionPasskey())
		}
		return r.Admin, nil
	case ReplyRoutingAck:
		if !r.Successful() {
			reason := pb.Routing_NONE
			if r.Routing != nil {
				reason = r.Routing.GetErrorReason()
			}
			// Stale or expired session passkey -- drop the cached
			// value so the next attempt re-establishes via Get.
			if reason == pb.Routing_ADMIN_BAD_SESSION_KEY && expectedFrom != 0 {
				s.invalidateSessionPasskey(expectedFrom)
			}
			return nil, fmt.Errorf("%s: %s (%s)", kind, routingErrorMessage(reason), reason.String())
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("%s: unexpected reply kind %v", kind, r.Kind)
	}
}

// routingErrorMessage maps a Meshtastic Routing.Error to an
// operator-facing description for PKC remote-admin failures. The error
// returned to the API also includes the raw enum name in parentheses so
// firmware-level debugging stays possible. Comments next to each case
// describe the most common cause in the trust-roster context.
func routingErrorMessage(reason pb.Routing_Error) string {
	switch reason {
	case pb.Routing_NONE:
		return "ok"
	case pb.Routing_NO_ROUTE:
		return "no mesh route to target -- node may be offline or out of range"
	case pb.Routing_GOT_NAK:
		return "intermediate hop NAK'd the packet"
	case pb.Routing_TIMEOUT:
		return "mesh hop timed out -- node may be off-air"
	case pb.Routing_NO_INTERFACE:
		return "no radio interface available for delivery"
	case pb.Routing_MAX_RETRANSMIT:
		return "max retries exhausted -- target not responding on the mesh"
	case pb.Routing_NO_CHANNEL:
		// Firmware bounces PKC admin packets as NO_CHANNEL when it
		// can't derive a matching channel hash. The most common cause
		// in a fleet context is an empty admin_key list on the target;
		// the firmware needs at least one admin pubkey to compute the
		// candidate PKC channel hash for an inbound admin packet.
		return "target has no matching admin channel -- check that CC's pubkey is in its security.admin_key list and the PRIMARY PSK matches"
	case pb.Routing_TOO_LARGE:
		return "admin packet exceeds the radio MTU after encoding"
	case pb.Routing_NO_RESPONSE:
		return "target received the request but did not reply (service unavailable or bad channel permissions)"
	case pb.Routing_DUTY_CYCLE_LIMIT:
		return "duty-cycle regulator blocked send -- retry shortly"
	case pb.Routing_BAD_REQUEST:
		return "target rejected the admin request as malformed"
	case pb.Routing_NOT_AUTHORIZED:
		return "target rejected as not authorized -- packet must arrive on the bound admin channel"
	case pb.Routing_PKI_FAILED:
		return "local PKC encryption failed -- no usable pubkey for target"
	case pb.Routing_PKI_UNKNOWN_PUBKEY:
		return "target lacks our pubkey -- let NodeInfo broadcast on the PRIMARY channel before retrying"
	case pb.Routing_ADMIN_BAD_SESSION_KEY:
		return "session passkey stale or expired -- the cached key was dropped, retry the verify"
	case pb.Routing_ADMIN_PUBLIC_KEY_UNAUTHORIZED:
		return "CC's admin pubkey is not in target's security.admin_key list"
	case pb.Routing_RATE_LIMIT_EXCEEDED:
		return "airtime rate limit exceeded -- retry shortly"
	case pb.Routing_PKI_SEND_FAIL_PUBLIC_KEY:
		return "no PKC pubkey for target known locally -- wait for NodeInfo or rerun manufacture"
	default:
		return "unrecognised mesh routing error"
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
