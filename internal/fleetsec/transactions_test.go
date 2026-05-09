package fleetsec

import (
	"context"
	"testing"
	"time"

	pb "github.com/karamble/diginode-cc/internal/meshpb"
	"google.golang.org/protobuf/proto"
)

func TestTracker_RoutingAckDelivery(t *testing.T) {
	tr := NewTracker()
	ch, err := tr.Begin(context.Background(), 0xdeadbeef, "test-routing", time.Second, false, 0)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if got := tr.Pending(); got != 1 {
		t.Errorf("Pending = %d, want 1", got)
	}

	// Encode a Routing with NONE error.
	routing := &pb.Routing{Variant: &pb.Routing_ErrorReason{ErrorReason: pb.Routing_NONE}}
	payload, err := proto.Marshal(routing)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	tr.HandleRoutingAck(0xa1b2c3d4, 0xdeadbeef, payload)

	select {
	case r := <-ch:
		if r.Kind != ReplyRoutingAck {
			t.Errorf("Kind = %v, want ReplyRoutingAck", r.Kind)
		}
		if r.From != 0xa1b2c3d4 {
			t.Errorf("From = %x, want a1b2c3d4", r.From)
		}
		if !r.Successful() {
			t.Error("Successful() = false on a NONE-error routing ack")
		}
	case <-time.After(time.Second):
		t.Fatal("no reply delivered")
	}

	if got := tr.Pending(); got != 0 {
		t.Errorf("Pending after delivery = %d, want 0", got)
	}
}

func TestTracker_AdminReplyDelivery(t *testing.T) {
	tr := NewTracker()
	ch, _ := tr.Begin(context.Background(), 0xfeedface, "test-admin", time.Second, true, 0)

	// AdminMessage with a get_channel_response inside.
	admin := &pb.AdminMessage{
		PayloadVariant: &pb.AdminMessage_GetChannelResponse{
			GetChannelResponse: &pb.Channel{
				Index: 0,
				Role:  pb.Channel_PRIMARY,
				Settings: &pb.ChannelSettings{Name: "primary", Psk: []byte{0xab, 0xcd}},
			},
		},
	}
	payload, _ := proto.Marshal(admin)

	tr.HandleAdminReply(0x11223344, 0xfeedface, payload)

	select {
	case r := <-ch:
		if r.Kind != ReplyAdminMsg {
			t.Errorf("Kind = %v, want ReplyAdminMsg", r.Kind)
		}
		if r.Admin == nil {
			t.Fatal("Admin nil")
		}
		got := r.Admin.GetGetChannelResponse()
		if got == nil {
			t.Fatal("GetChannelResponse variant missing")
		}
		if got.GetSettings().GetName() != "primary" {
			t.Errorf("name = %q", got.GetSettings().GetName())
		}
	case <-time.After(time.Second):
		t.Fatal("no reply delivered")
	}
}

func TestTracker_Timeout(t *testing.T) {
	tr := NewTracker()
	ch, _ := tr.Begin(context.Background(), 0x12345678, "timeout-test", 50*time.Millisecond, false, 0)

	select {
	case r := <-ch:
		if r.Kind != ReplyTimeout {
			t.Errorf("Kind = %v, want ReplyTimeout", r.Kind)
		}
		if r.Err == nil {
			t.Error("Err nil on timeout")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout never fired")
	}

	if got := tr.Pending(); got != 0 {
		t.Errorf("Pending after timeout = %d, want 0", got)
	}
}

func TestTracker_ContextCancel(t *testing.T) {
	tr := NewTracker()
	ctx, cancel := context.WithCancel(context.Background())
	ch, _ := tr.Begin(ctx, 0x11111111, "cancel-test", time.Hour, false, 0)

	cancel()

	select {
	case r := <-ch:
		if r.Kind != ReplyCancelled {
			t.Errorf("Kind = %v, want ReplyCancelled", r.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel never propagated")
	}
}

func TestTracker_RejectsZeroID(t *testing.T) {
	tr := NewTracker()
	if _, err := tr.Begin(context.Background(), 0, "test", time.Second, false, 0); err == nil {
		t.Error("Begin(0) accepted")
	}
}

func TestTracker_RejectsDuplicate(t *testing.T) {
	tr := NewTracker()
	if _, err := tr.Begin(context.Background(), 42, "test", time.Second, false, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Begin(context.Background(), 42, "test", time.Second, false, 0); err == nil {
		t.Error("duplicate Begin accepted")
	}
}

func TestTracker_DropUnknownAck(t *testing.T) {
	tr := NewTracker()
	// No transactions registered. Calling Handle should be a no-op (not panic).
	tr.HandleRoutingAck(1, 0xdeadbeef, nil)
	tr.HandleAdminReply(1, 0xdeadbeef, nil)
	if got := tr.Pending(); got != 0 {
		t.Errorf("Pending = %d, want 0", got)
	}
}

// TestTracker_GetRequest_RoutingAckIgnoredOnSuccess pins the fix for
// the "nil admin reply" race: when expectsAdminReply=true, a successful
// Routing ack must NOT resolve the tx -- the AdminMessage that follows
// is what carries the data and what the caller is waiting for.
func TestTracker_GetRequest_RoutingAckIgnoredOnSuccess(t *testing.T) {
	tr := NewTracker()
	ch, _ := tr.Begin(context.Background(), 0xcafef00d, "get-config", time.Second, true, 0)

	routing := &pb.Routing{Variant: &pb.Routing_ErrorReason{ErrorReason: pb.Routing_NONE}}
	rPayload, _ := proto.Marshal(routing)
	tr.HandleRoutingAck(0xa1b2, 0xcafef00d, rPayload)

	// Successful ack on a Get-style tx must leave it pending.
	if got := tr.Pending(); got != 1 {
		t.Fatalf("Pending after success ack = %d, want 1 (tx should still wait for AdminMessage)", got)
	}
	select {
	case r := <-ch:
		t.Fatalf("unexpected early reply: kind=%v admin=%v", r.Kind, r.Admin)
	case <-time.After(50 * time.Millisecond):
	}

	// AdminMessage arrives second; that's what should resolve the tx.
	admin := &pb.AdminMessage{
		PayloadVariant: &pb.AdminMessage_GetConfigResponse{
			GetConfigResponse: &pb.Config{
				PayloadVariant: &pb.Config_Security{
					Security: &pb.Config_SecurityConfig{
						PublicKey: []byte{0x01, 0x02, 0x03},
					},
				},
			},
		},
	}
	aPayload, _ := proto.Marshal(admin)
	tr.HandleAdminReply(0xa1b2, 0xcafef00d, aPayload)

	select {
	case r := <-ch:
		if r.Kind != ReplyAdminMsg {
			t.Errorf("Kind = %v, want ReplyAdminMsg", r.Kind)
		}
		if r.Admin == nil {
			t.Fatal("Admin nil")
		}
		sec := r.Admin.GetGetConfigResponse().GetSecurity()
		if sec == nil || len(sec.GetPublicKey()) != 3 {
			t.Errorf("payload didn't survive: %+v", r.Admin)
		}
	case <-time.After(time.Second):
		t.Fatal("AdminMessage didn't resolve the tx")
	}

	if got := tr.Pending(); got != 0 {
		t.Errorf("Pending after admin reply = %d, want 0", got)
	}
}

// TestTracker_GetRequest_RoutingAckResolvesOnFailure: even on a Get-style
// tx, a routing ack with a non-NONE error_reason must resolve the tx
// immediately. No AdminMessage will follow if the firmware refused.
func TestTracker_GetRequest_RoutingAckResolvesOnFailure(t *testing.T) {
	tr := NewTracker()
	ch, _ := tr.Begin(context.Background(), 0xbadbad01, "get-config-fail", time.Second, true, 0)

	routing := &pb.Routing{Variant: &pb.Routing_ErrorReason{ErrorReason: pb.Routing_NO_ROUTE}}
	payload, _ := proto.Marshal(routing)
	tr.HandleRoutingAck(0xa1b2, 0xbadbad01, payload)

	select {
	case r := <-ch:
		if r.Kind != ReplyRoutingAck {
			t.Errorf("Kind = %v, want ReplyRoutingAck", r.Kind)
		}
		if r.Successful() {
			t.Error("Successful() = true on NO_ROUTE")
		}
	case <-time.After(time.Second):
		t.Fatal("routing failure didn't resolve the tx")
	}

	if got := tr.Pending(); got != 0 {
		t.Errorf("Pending after failure ack = %d, want 0", got)
	}
}

// TestTracker_WrongFromIsDropped pins the false-success fix. The
// Pi-Heltec emits a from=local routing ack ("transmitted it") for every
// outbound packet -- if the tracker accepted that ack as confirmation
// for a remote PKC Set*, the rotation worker would falsely succeed
// against an unpowered remote. Begin with expectedFrom = remote-num and
// confirm a routing ack from another sender does not resolve the tx.
func TestTracker_WrongFromIsDropped(t *testing.T) {
	tr := NewTracker()
	const target uint32 = 0x0409cf64 // pretend HB35
	ch, _ := tr.Begin(context.Background(), 0xacef0001, "remote-set", 200*time.Millisecond, false, target)

	// Local-loopback "I sent it" ack (different from).
	routing := &pb.Routing{Variant: &pb.Routing_ErrorReason{ErrorReason: pb.Routing_NONE}}
	payload, _ := proto.Marshal(routing)
	tr.HandleRoutingAck(0x0409c9d8, 0xacef0001, payload) // local Pi-Heltec, not the target

	// Tx must still be pending.
	if got := tr.Pending(); got != 1 {
		t.Fatalf("Pending after wrong-from ack = %d, want 1 (loopback ack must be dropped)", got)
	}

	// Real ack from the target resolves it.
	tr.HandleRoutingAck(target, 0xacef0001, payload)
	select {
	case r := <-ch:
		if r.Kind != ReplyRoutingAck {
			t.Errorf("Kind = %v, want ReplyRoutingAck", r.Kind)
		}
		if r.From != target {
			t.Errorf("From = %x, want %x", r.From, target)
		}
	case <-time.After(time.Second):
		t.Fatal("real target ack didn't resolve tx")
	}
}

// TestTracker_ZeroFromAcceptsAny preserves the legacy behavior for
// existing tests that don't care about source filtering.
func TestTracker_ZeroFromAcceptsAny(t *testing.T) {
	tr := NewTracker()
	ch, _ := tr.Begin(context.Background(), 0xb00bf00d, "any-source", time.Second, false, 0)

	routing := &pb.Routing{Variant: &pb.Routing_ErrorReason{ErrorReason: pb.Routing_NONE}}
	payload, _ := proto.Marshal(routing)
	tr.HandleRoutingAck(0xdeadbeef, 0xb00bf00d, payload) // any from is fine

	select {
	case r := <-ch:
		if r.Kind != ReplyRoutingAck {
			t.Errorf("Kind = %v, want ReplyRoutingAck", r.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("ack with expectedFrom=0 didn't resolve")
	}
}

func TestTracker_DropZeroRequestID(t *testing.T) {
	tr := NewTracker()
	ch, _ := tr.Begin(context.Background(), 1, "test", 200*time.Millisecond, false, 0)

	// Acks with request_id=0 should NOT match anything (broadcast/unsolicited).
	tr.HandleRoutingAck(1, 0, nil)
	tr.HandleAdminReply(1, 0, nil)

	// Confirm the still-pending tx is unaffected (should time out normally).
	select {
	case r := <-ch:
		if r.Kind != ReplyTimeout {
			t.Errorf("expected timeout, got %v", r.Kind)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout never fired")
	}
}
