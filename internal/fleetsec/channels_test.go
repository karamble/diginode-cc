package fleetsec

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/karamble/diginode-cc/internal/meshpb"
)

// --- Pure function tests (no Service / store / serial wiring) ---

func TestStatusForPhase(t *testing.T) {
	cases := []struct {
		phase RotationPhase
		want  TargetStatus
	}{
		{PhasePending, TargetStatusPending},
		{PhasePushingB, TargetStatusInFlight},
		{PhaseHasNewPSK, TargetStatusInFlight},
		{PhasePromotingC, TargetStatusInFlight},
		{PhaseOnNewPSK, TargetStatusAcked},
		{PhaseRetired, TargetStatusAcked},
		{PhaseFailedB, TargetStatusFailed},
		{PhaseFailedC, TargetStatusFailed},
	}
	for _, c := range cases {
		t.Run(string(c.phase), func(t *testing.T) {
			if got := statusForPhase(c.phase); got != c.want {
				t.Errorf("statusForPhase(%s) = %s, want %s", c.phase, got, c.want)
			}
		})
	}
}

func TestPhaseForLegacyStatus(t *testing.T) {
	cases := []struct {
		status TargetStatus
		want   RotationPhase
	}{
		{TargetStatusPending, PhasePending},
		{TargetStatusInFlight, PhasePushingB},
		{TargetStatusAcked, PhaseOnNewPSK},
		{TargetStatusFailed, PhaseFailedB},
	}
	for _, c := range cases {
		t.Run(string(c.status), func(t *testing.T) {
			if got := phaseForLegacyStatus(c.status); got != c.want {
				t.Errorf("phaseForLegacyStatus(%s) = %s, want %s", c.status, got, c.want)
			}
		})
	}
}

func TestChooseStagingSlot(t *testing.T) {
	s := &Service{}
	// Default flow: PRIMARY on slot 0 -> staging picks slot 1.
	got, err := s.chooseStagingSlot(0)
	if err != nil {
		t.Fatalf("chooseStagingSlot(0): %v", err)
	}
	if got != 1 {
		t.Errorf("PRIMARY=0 staging = %d, want 1", got)
	}

	// Edge: PRIMARY already on slot 1 -> staging falls back to slot 2 so
	// we don't overwrite the active channel.
	got, err = s.chooseStagingSlot(1)
	if err != nil {
		t.Fatalf("chooseStagingSlot(1): %v", err)
	}
	if got != 2 {
		t.Errorf("PRIMARY=1 staging = %d, want 2", got)
	}
}

// --- Helper tests using fakeSerial (no DB; tests phase-A/B/C admin shapes) ---

// canned scripts the helper send sequence against. fakeSerial replays the
// canned replies in order; helperReplay lets us assert the sequence of
// outbound AdminMessage variants the helpers emitted, by walking
// fs.history.
type sentAdmin struct {
	to       uint32
	variant  string // payload variant type name, e.g., "GetConfigRequest"
	chanIdx  int32
	chanRole pb.Channel_Role
}

// captureAdmins parses fs.last as a ToRadio + decodes the inner Data
// payload as an AdminMessage. Returns the variant type name + any
// SetChannel index/role info for assertion. Used by helper tests to
// verify the right wire calls went out.
func captureAdmin(data []byte, t *testing.T) sentAdmin {
	t.Helper()
	var tr pb.ToRadio
	if err := proto.Unmarshal(data, &tr); err != nil {
		t.Fatalf("unmarshal ToRadio: %v", err)
	}
	pkt := tr.GetPacket()
	if pkt == nil {
		t.Fatal("no packet in ToRadio")
	}
	dec := pkt.GetDecoded()
	if dec == nil {
		t.Fatal("no decoded data in MeshPacket")
	}
	var admin pb.AdminMessage
	if err := proto.Unmarshal(dec.GetPayload(), &admin); err != nil {
		t.Fatalf("unmarshal AdminMessage: %v", err)
	}
	out := sentAdmin{to: pkt.GetTo()}
	switch v := admin.GetPayloadVariant().(type) {
	case *pb.AdminMessage_GetConfigRequest:
		out.variant = "GetConfigRequest"
	case *pb.AdminMessage_SetChannel:
		out.variant = "SetChannel"
		out.chanIdx = v.SetChannel.GetIndex()
		out.chanRole = v.SetChannel.GetRole()
	default:
		out.variant = "other"
	}
	return out
}

func TestApplyLocalStagingChannel_SendsSetChannelSecondary(t *testing.T) {
	s, _, fs := makeTestService(t)
	// Local SetChannel is a Set/command -- only routing ack expected.
	fs.reply = &Reply{
		Kind:    ReplyRoutingAck,
		Routing: &pb.Routing{Variant: &pb.Routing_ErrorReason{ErrorReason: pb.Routing_NONE}},
	}
	const stagingIdx int32 = 1
	psk := []byte{0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f}

	if err := s.applyLocalStagingChannel(context.Background(), stagingIdx, psk); err != nil {
		t.Fatalf("applyLocalStagingChannel: %v", err)
	}
	got := captureAdmin(fs.last, t)
	if got.variant != "SetChannel" {
		t.Fatalf("variant = %s, want SetChannel", got.variant)
	}
	if got.chanIdx != stagingIdx {
		t.Errorf("channel idx = %d, want %d", got.chanIdx, stagingIdx)
	}
	if got.chanRole != pb.Channel_SECONDARY {
		t.Errorf("channel role = %v, want SECONDARY", got.chanRole)
	}
	// Local admin destination = local node num.
	if got.to != 0xdeadbeef {
		t.Errorf("to = %x, want %x", got.to, 0xdeadbeef)
	}
}

func TestPushStagingToRemote_SendsGetThenSetSecondary(t *testing.T) {
	s, _, fs := makeTestService(t)
	// Need a stateful fakeSerial: respond with a Get reply for the first
	// Send (Get) and a routing ack for the second (Set). Build with two
	// canned replies queued.
	const remote uint32 = 0xa1b2c3d4
	wantPasskey := []byte{0x42}
	fs.replyQueue = []Reply{
		// First: Get reply with passkey
		{Kind: ReplyAdminMsg, Admin: &pb.AdminMessage{
			SessionPasskey: wantPasskey,
			PayloadVariant: &pb.AdminMessage_GetConfigResponse{
				GetConfigResponse: &pb.Config{
					PayloadVariant: &pb.Config_Security{Security: &pb.Config_SecurityConfig{}},
				},
			},
		}},
		// Second: routing ack for SetChannel
		{Kind: ReplyRoutingAck, Routing: &pb.Routing{Variant: &pb.Routing_ErrorReason{ErrorReason: pb.Routing_NONE}}},
	}
	const stagingIdx int32 = 1
	psk := []byte{0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f}

	if err := s.pushStagingToRemote(context.Background(), remote, stagingIdx, psk); err != nil {
		t.Fatalf("pushStagingToRemote: %v", err)
	}
	if len(fs.history) != 2 {
		t.Fatalf("history len = %d, want 2 (Get + Set)", len(fs.history))
	}
	first := captureAdmin(fs.history[0], t)
	second := captureAdmin(fs.history[1], t)
	if first.variant != "GetConfigRequest" {
		t.Errorf("first admin = %s, want GetConfigRequest", first.variant)
	}
	if second.variant != "SetChannel" || second.chanIdx != stagingIdx || second.chanRole != pb.Channel_SECONDARY {
		t.Errorf("second admin = %+v, want SetChannel(idx=%d, SECONDARY)", second, stagingIdx)
	}
	if got := s.getSessionPasskey(remote); !bytesEq(got, wantPasskey) {
		t.Errorf("session passkey not cached: got %x want %x", got, wantPasskey)
	}
}

func TestPromoteRemoteToNewPrimary_SendsSetChannelPrimary(t *testing.T) {
	s, _, fs := makeTestService(t)
	const remote uint32 = 0xa1b2c3d4
	fs.replyQueue = []Reply{
		// Get reply with passkey
		{Kind: ReplyAdminMsg, Admin: &pb.AdminMessage{
			SessionPasskey: []byte{0x99},
			PayloadVariant: &pb.AdminMessage_GetConfigResponse{
				GetConfigResponse: &pb.Config{
					PayloadVariant: &pb.Config_Security{Security: &pb.Config_SecurityConfig{}},
				},
			},
		}},
		// Routing ack for promote
		{Kind: ReplyRoutingAck, Routing: &pb.Routing{Variant: &pb.Routing_ErrorReason{ErrorReason: pb.Routing_NONE}}},
	}
	const stagingIdx int32 = 1
	psk := []byte{0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f}

	if err := s.promoteRemoteToNewPrimary(context.Background(), remote, stagingIdx, psk); err != nil {
		t.Fatalf("promoteRemoteToNewPrimary: %v", err)
	}
	if len(fs.history) != 2 {
		t.Fatalf("history len = %d, want 2 (Get + Set-PRIMARY)", len(fs.history))
	}
	second := captureAdmin(fs.history[1], t)
	if second.variant != "SetChannel" || second.chanIdx != stagingIdx || second.chanRole != pb.Channel_PRIMARY {
		t.Errorf("promote admin = %+v, want SetChannel(idx=%d, PRIMARY)", second, stagingIdx)
	}
}

func TestPushStagingToRemote_GetFailsAborts(t *testing.T) {
	s, _, fs := makeTestService(t)
	// Get returns a NO_ROUTE routing error; SetChannel must NOT be sent.
	fs.reply = &Reply{
		Kind:    ReplyRoutingAck,
		Routing: &pb.Routing{Variant: &pb.Routing_ErrorReason{ErrorReason: pb.Routing_NO_ROUTE}},
	}
	const remote uint32 = 0xa1b2c3d4
	psk := make([]byte, 16)

	err := s.pushStagingToRemote(context.Background(), remote, 1, psk)
	if err == nil {
		t.Fatal("expected error from failed Get")
	}
	if !errorContains(err, "session establish") {
		t.Errorf("error = %v, want session establish wrapper", err)
	}
	// Only one packet should have left the wire (the failed Get).
	if len(fs.history) != 1 {
		t.Errorf("history len = %d, want 1 (only the failed Get; Set must not have been sent)", len(fs.history))
	}
}

func errorContains(err error, sub string) bool {
	return err != nil && (err.Error() == sub || (len(err.Error()) >= len(sub) && containsString(err.Error(), sub)))
}

func containsString(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// --- Avoid unused import if helper tests don't reference these ---
var _ = errors.New
var _ = time.Second
