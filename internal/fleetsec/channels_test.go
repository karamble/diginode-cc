package fleetsec

import (
	"bytes"
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
	case *pb.AdminMessage_GetChannelRequest:
		out.variant = "GetChannelRequest"
		// Wire is (slot+1); decode back to slot index for assertions.
		out.chanIdx = int32(v.GetChannelRequest) - 1
	case *pb.AdminMessage_SetChannel:
		out.variant = "SetChannel"
		out.chanIdx = v.SetChannel.GetIndex()
		out.chanRole = v.SetChannel.GetRole()
	case *pb.AdminMessage_BeginEditSettings:
		out.variant = "BeginEditSettings"
	case *pb.AdminMessage_CommitEditSettings:
		out.variant = "CommitEditSettings"
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


// TestMigrateRemoteAtomic_Sends5FrameSequence covers the per-remote
// atomic rotation primitive. The sequence is: Get (passkey establish)
// -> Begin -> Set(stage, PRIMARY, new) -> Set(old, DISABLED, empty)
// -> Commit -> Get (verify probe). Total 6 outbound frames: 5
// transaction + 1 post-commit verify. CRITICAL: no intermediate
// reads inside the begin/commit window. The commit ack is dropped
// firmware-side for PKI command-style verbs, so the controller
// validates by reading the staging slot back instead of waiting on
// a routing ack that can never arrive.
func TestMigrateRemoteAtomic_Sends5FrameSequence(t *testing.T) {
	// Shrink verify timings so the test doesn't sleep 12+s.
	prevWait, prevDeadline, prevBackoff := commitVerifyInitialWait, commitVerifyDeadline, commitVerifyBackoff
	commitVerifyInitialWait = 1 * time.Millisecond
	commitVerifyDeadline = 500 * time.Millisecond
	commitVerifyBackoff = 10 * time.Millisecond
	t.Cleanup(func() {
		commitVerifyInitialWait, commitVerifyDeadline, commitVerifyBackoff = prevWait, prevDeadline, prevBackoff
	})

	s, _, fs := makeTestService(t)
	const remote uint32 = 0xa1b2c3d4
	const stagingIdx int32 = 1
	const oldSlot int32 = 0
	wantPasskey := []byte{0x77}
	psk := bytes.Repeat([]byte{0xab}, 16)
	pskFP := Fingerprint(psk)
	_ = pskFP // verify probe matches against this in the reply below
	fs.replyQueue = []Reply{
		// Get for session-passkey establish
		{Kind: ReplyAdminMsg, Admin: &pb.AdminMessage{
			SessionPasskey: wantPasskey,
			PayloadVariant: &pb.AdminMessage_GetChannelResponse{
				GetChannelResponse: &pb.Channel{Index: 0, Role: pb.Channel_PRIMARY},
			},
		}},
		// Begin / Set / Set / Commit are fire-and-forget; SendToRadio still
		// pops a queue entry per Send, so feed harmless empty routing acks.
		{Kind: ReplyRoutingAck, Routing: &pb.Routing{Variant: &pb.Routing_ErrorReason{ErrorReason: pb.Routing_NONE}}},
		{Kind: ReplyRoutingAck, Routing: &pb.Routing{Variant: &pb.Routing_ErrorReason{ErrorReason: pb.Routing_NONE}}},
		{Kind: ReplyRoutingAck, Routing: &pb.Routing{Variant: &pb.Routing_ErrorReason{ErrorReason: pb.Routing_NONE}}},
		{Kind: ReplyRoutingAck, Routing: &pb.Routing{Variant: &pb.Routing_ErrorReason{ErrorReason: pb.Routing_NONE}}},
		// Post-commit verify probe: get_channel(stagingIdx) returns the
		// expected PRIMARY+newPSK state.
		{Kind: ReplyAdminMsg, Admin: &pb.AdminMessage{
			SessionPasskey: wantPasskey,
			PayloadVariant: &pb.AdminMessage_GetChannelResponse{
				GetChannelResponse: &pb.Channel{
					Index: stagingIdx,
					Role:  pb.Channel_PRIMARY,
					Settings: &pb.ChannelSettings{
						Psk: psk,
					},
				},
			},
		}},
	}

	if err := s.migrateRemoteAtomic(context.Background(), remote, stagingIdx, oldSlot, psk); err != nil {
		t.Fatalf("migrateRemoteAtomic: %v", err)
	}
	if len(fs.history) != 6 {
		t.Fatalf("history len = %d, want 6 (Get + Begin + 2*Set + Commit + verify Get)", len(fs.history))
	}
	get := captureAdmin(fs.history[0], t)
	begin := captureAdmin(fs.history[1], t)
	setPri := captureAdmin(fs.history[2], t)
	setDis := captureAdmin(fs.history[3], t)
	commit := captureAdmin(fs.history[4], t)
	verify := captureAdmin(fs.history[5], t)
	if get.variant != "GetChannelRequest" {
		t.Errorf("frame 0 = %s, want GetChannelRequest", get.variant)
	}
	if begin.variant != "BeginEditSettings" {
		t.Errorf("frame 1 = %s, want BeginEditSettings", begin.variant)
	}
	if setPri.variant != "SetChannel" || setPri.chanIdx != stagingIdx || setPri.chanRole != pb.Channel_PRIMARY {
		t.Errorf("frame 2 = %+v, want SetChannel(idx=%d, PRIMARY)", setPri, stagingIdx)
	}
	if setDis.variant != "SetChannel" || setDis.chanIdx != oldSlot || setDis.chanRole != pb.Channel_DISABLED {
		t.Errorf("frame 3 = %+v, want SetChannel(idx=%d, DISABLED)", setDis, oldSlot)
	}
	if commit.variant != "CommitEditSettings" {
		t.Errorf("frame 4 = %s, want CommitEditSettings", commit.variant)
	}
	if verify.variant != "GetChannelRequest" {
		t.Errorf("frame 5 = %s, want GetChannelRequest (post-commit verify)", verify.variant)
	}
}

// TestMigratePiAtomic_Sends4FrameSequence covers the local-side
// equivalent. No initial Get (local admin path; passkey isn't checked
// the same way for the loopback transport, and we don't need to refresh
// it here because the rotation worker already issued admin frames
// during Phase A). Begin -> Set(stage, PRIMARY, new) -> Set(old,
// DISABLED, empty) -> Commit. 4 outbound frames.
func TestMigratePiAtomic_Sends4FrameSequence(t *testing.T) {
	s, _, fs := makeTestService(t)
	const stagingIdx int32 = 1
	const oldSlot int32 = 0
	fs.replyQueue = []Reply{
		{Kind: ReplyRoutingAck, Routing: &pb.Routing{Variant: &pb.Routing_ErrorReason{ErrorReason: pb.Routing_NONE}}},
		{Kind: ReplyRoutingAck, Routing: &pb.Routing{Variant: &pb.Routing_ErrorReason{ErrorReason: pb.Routing_NONE}}},
		{Kind: ReplyRoutingAck, Routing: &pb.Routing{Variant: &pb.Routing_ErrorReason{ErrorReason: pb.Routing_NONE}}},
		{Kind: ReplyRoutingAck, Routing: &pb.Routing{Variant: &pb.Routing_ErrorReason{ErrorReason: pb.Routing_NONE}}},
	}
	psk := bytes.Repeat([]byte{0xcd}, 16)

	if err := s.migratePiAtomic(context.Background(), stagingIdx, oldSlot, psk); err != nil {
		t.Fatalf("migratePiAtomic: %v", err)
	}
	if len(fs.history) != 4 {
		t.Fatalf("history len = %d, want 4 (Begin + 2*Set + Commit)", len(fs.history))
	}
	begin := captureAdmin(fs.history[0], t)
	setPri := captureAdmin(fs.history[1], t)
	setDis := captureAdmin(fs.history[2], t)
	commit := captureAdmin(fs.history[3], t)
	if begin.variant != "BeginEditSettings" {
		t.Errorf("frame 0 = %s, want BeginEditSettings", begin.variant)
	}
	if setPri.variant != "SetChannel" || setPri.chanIdx != stagingIdx || setPri.chanRole != pb.Channel_PRIMARY {
		t.Errorf("frame 1 = %+v, want SetChannel(idx=%d, PRIMARY)", setPri, stagingIdx)
	}
	if setDis.variant != "SetChannel" || setDis.chanIdx != oldSlot || setDis.chanRole != pb.Channel_DISABLED {
		t.Errorf("frame 2 = %+v, want SetChannel(idx=%d, DISABLED)", setDis, oldSlot)
	}
	if commit.variant != "CommitEditSettings" {
		t.Errorf("frame 3 = %s, want CommitEditSettings", commit.variant)
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
