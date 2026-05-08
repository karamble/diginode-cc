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
	ch, err := tr.Begin(context.Background(), 0xdeadbeef, "test-routing", time.Second)
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
	ch, _ := tr.Begin(context.Background(), 0xfeedface, "test-admin", time.Second)

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
	ch, _ := tr.Begin(context.Background(), 0x12345678, "timeout-test", 50*time.Millisecond)

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
	ch, _ := tr.Begin(ctx, 0x11111111, "cancel-test", time.Hour)

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
	if _, err := tr.Begin(context.Background(), 0, "test", time.Second); err == nil {
		t.Error("Begin(0) accepted")
	}
}

func TestTracker_RejectsDuplicate(t *testing.T) {
	tr := NewTracker()
	if _, err := tr.Begin(context.Background(), 42, "test", time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Begin(context.Background(), 42, "test", time.Second); err == nil {
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

func TestTracker_DropZeroRequestID(t *testing.T) {
	tr := NewTracker()
	ch, _ := tr.Begin(context.Background(), 1, "test", 200*time.Millisecond)

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
