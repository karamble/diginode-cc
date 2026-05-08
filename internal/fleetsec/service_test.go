package fleetsec

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/karamble/diginode-cc/internal/meshpb"
)

// fakeSerial captures the most recent ToRadio frame and (after caller
// invokes RouteAck or RouteAdminReply) feeds it back through the
// service's tracker so service code that awaits a reply unblocks.
type fakeSerial struct {
	mu      sync.Mutex
	tracker *Tracker
	last    []byte
	// reply is the canned reply the tracker should deliver next.
	// If nil, no auto-reply (caller must drive the tracker manually).
	reply *Reply
}

func newFakeSerial(tr *Tracker, reply *Reply) *fakeSerial {
	return &fakeSerial{tracker: tr, reply: reply}
}

func (f *fakeSerial) SendToRadio(data []byte) error {
	f.mu.Lock()
	f.last = append([]byte(nil), data...)
	reply := f.reply
	f.mu.Unlock()

	if reply == nil {
		return nil
	}
	// Decode the packet ID from the ToRadio frame so we know which
	// transaction to feed.
	var tr pb.ToRadio
	if err := proto.Unmarshal(data, &tr); err != nil {
		return err
	}
	pkt := tr.GetPacket()
	if pkt == nil {
		return errors.New("no packet variant in fake send")
	}
	pid := pkt.GetId()
	go func(id uint32, r Reply) {
		// Deliver a ROUTING ack with the packet's id as request_id.
		// Marshal the canned reply payload.
		var payload []byte
		if r.Routing != nil {
			payload, _ = proto.Marshal(r.Routing)
			f.tracker.HandleRoutingAck(0xa1b2c3d4, id, payload)
		} else if r.Admin != nil {
			payload, _ = proto.Marshal(r.Admin)
			f.tracker.HandleAdminReply(0xa1b2c3d4, id, payload)
		}
	}(pid, *reply)
	return nil
}

type fakeLocalNode struct{ num uint32 }

func (f *fakeLocalNode) LocalNodeNum() uint32 { return f.num }

// makeTestService constructs a Service wired with fakes. tracker is
// returned so tests can drive it manually; serial is returned so tests
// can inspect what was sent.
func makeTestService(t *testing.T) (*Service, *Tracker, *fakeSerial) {
	t.Helper()
	tr := NewTracker()
	fs := newFakeSerial(tr, nil)
	s := &Service{
		tracker:   tr,
		serial:    fs,
		localNode: &fakeLocalNode{num: 0xdeadbeef},
		// store and audit are nil in this test -- methods that use them
		// must be exercised separately with an integration store.
	}
	return s, tr, fs
}

func TestService_LocalNodeUnknownReturnsError(t *testing.T) {
	tr := NewTracker()
	s := &Service{
		tracker:   tr,
		serial:    newFakeSerial(tr, nil),
		localNode: &fakeLocalNode{num: 0},
	}
	_, err := s.runLocalAdmin(context.Background(), AdminGetConfig(pb.AdminMessage_SECURITY_CONFIG), "test")
	if !errors.Is(err, ErrLocalNodeUnknown) {
		t.Errorf("got %v, want ErrLocalNodeUnknown", err)
	}
}

func TestService_RunLocalAdmin_RoutingAckSuccess(t *testing.T) {
	s, _, fs := makeTestService(t)
	fs.reply = &Reply{
		Kind:    ReplyRoutingAck,
		Routing: &pb.Routing{Variant: &pb.Routing_ErrorReason{ErrorReason: pb.Routing_NONE}},
	}

	// Set/command messages legitimately resolve on the routing ack
	// alone -- firmware doesn't follow with an AdminMessage payload.
	reply, err := s.runLocalAdmin(context.Background(), AdminBeginEditSettings(), "test-ack")
	if err != nil {
		t.Fatalf("runLocalAdmin: %v", err)
	}
	if reply != nil {
		t.Errorf("expected nil reply for plain ack, got %v", reply)
	}
	if fs.last == nil {
		t.Error("nothing was sent")
	}
}

func TestService_RunLocalAdmin_RoutingAckError(t *testing.T) {
	s, _, fs := makeTestService(t)
	fs.reply = &Reply{
		Kind:    ReplyRoutingAck,
		Routing: &pb.Routing{Variant: &pb.Routing_ErrorReason{ErrorReason: pb.Routing_NO_RESPONSE}},
	}

	// Routing failures must resolve a Get-style tx immediately -- no
	// AdminMessage will follow if firmware refused the request.
	_, err := s.runLocalAdmin(context.Background(), AdminGetConfig(pb.AdminMessage_SECURITY_CONFIG), "test-err")
	if err == nil {
		t.Error("expected routing-error to surface as Go error")
	}
}

func TestService_RunRemoteAdmin_AdminReplyDelivered(t *testing.T) {
	s, _, fs := makeTestService(t)
	fs.reply = &Reply{
		Kind: ReplyAdminMsg,
		Admin: &pb.AdminMessage{
			PayloadVariant: &pb.AdminMessage_GetConfigResponse{
				GetConfigResponse: &pb.Config{
					PayloadVariant: &pb.Config_Security{
						Security: &pb.Config_SecurityConfig{IsManaged: true},
					},
				},
			},
		},
	}

	reply, err := s.runRemoteAdmin(context.Background(), 0xa1b2c3d4, AdminGetConfig(pb.AdminMessage_SECURITY_CONFIG), "test-remote")
	if err != nil {
		t.Fatalf("runRemoteAdmin: %v", err)
	}
	sec, err := extractSecurityConfig(reply)
	if err != nil {
		t.Fatalf("extractSecurityConfig: %v", err)
	}
	if !sec.GetIsManaged() {
		t.Error("expected IsManaged=true after round-trip")
	}
}

func TestRedactSecrets(t *testing.T) {
	in := map[string]any{
		"label":       "primary",
		"psk":         []byte("secret"),
		"private_key": "shhh",
		"fingerprint": "ab:cd:ef",
	}
	out := redactSecrets(in)
	if out["label"] != "primary" || out["fingerprint"] != "ab:cd:ef" {
		t.Errorf("non-secret keys mutated: %+v", out)
	}
	if out["psk"] != "<redacted>" || out["private_key"] != "<redacted>" {
		t.Errorf("secret keys not redacted: %+v", out)
	}
}

func TestComputeDriftStatus(t *testing.T) {
	now := timePtr()
	tests := []struct {
		name   string
		node   NodeTrustRecord
		policy *FleetPolicy
		want   DriftStatus
	}{
		{
			name: "never verified",
			node: NodeTrustRecord{},
			want: DriftStatusUnknown,
		},
		{
			name: "no policy = in policy",
			node: NodeTrustRecord{LastVerifiedAt: now},
			want: DriftStatusInPolicy,
		},
		{
			name:   "is_managed mismatch",
			node:   NodeTrustRecord{LastVerifiedAt: now, IsManaged: false},
			policy: &FleetPolicy{ExpectedIsManaged: true},
			want:   DriftStatusDrift,
		},
		{
			name: "missing expected key",
			node: NodeTrustRecord{LastVerifiedAt: now, AdminKeyFPs: []string{"aa:bb"}},
			policy: &FleetPolicy{
				ExpectedAdminKeyFPs: []string{"aa:bb", "cc:dd"},
			},
			want: DriftStatusDrift,
		},
		{
			name: "extra key tolerated",
			node: NodeTrustRecord{LastVerifiedAt: now,
				AdminKeyFPs: []string{"aa:bb", "extra"}},
			policy: &FleetPolicy{
				ExpectedAdminKeyFPs: []string{"aa:bb"},
			},
			want: DriftStatusInPolicy,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := computeDriftStatus(tc.node, tc.policy); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// timePtr returns a non-zero *time.Time for "node has been verified at
// least once" cases. The actual value doesn't matter -- computeDriftStatus
// only checks for nil.
func timePtr() *time.Time {
	t := time.Now()
	return &t
}
