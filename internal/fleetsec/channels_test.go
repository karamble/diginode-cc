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

// channelReply builds a ReplyAdmin for a single GetChannel(idx) call,
// canned with the given role. PSK is set to a placeholder so the parser
// path doesn't bail; chooseStagingSlot only inspects role.
func channelReply(idx int32, role pb.Channel_Role) Reply {
	ch := &pb.Channel{Index: idx, Role: role}
	if role != pb.Channel_DISABLED {
		ch.Settings = &pb.ChannelSettings{Psk: []byte{0x42}}
	}
	return Reply{
		Kind: ReplyAdminMsg,
		Admin: &pb.AdminMessage{
			PayloadVariant: &pb.AdminMessage_GetChannelResponse{GetChannelResponse: ch},
		},
	}
}

// TestChooseStagingSlot_SkipsActivePrimary covers the bug that bricked
// HB35 in §10.9 offline-test: when PRIMARY had moved to slot 1 from a
// prior rotation, the old static picker still returned slot 1 and the
// SetChannel(SECONDARY) overwrote PRIMARY in place, leaving the node
// with no PRIMARY = no radio. The probe-based picker MUST skip
// whatever slot currently holds PRIMARY.
func TestChooseStagingSlot_SkipsActivePrimary(t *testing.T) {
	s, _, fs := makeTestService(t)
	// Slot layout: PRIMARY on slot 1, every other slot DISABLED.
	// chooseStagingSlot probes slots 0..7 in order.
	fs.replyQueue = []Reply{
		channelReply(0, pb.Channel_DISABLED),
		channelReply(1, pb.Channel_PRIMARY),
		channelReply(2, pb.Channel_DISABLED),
		channelReply(3, pb.Channel_DISABLED),
		channelReply(4, pb.Channel_DISABLED),
		channelReply(5, pb.Channel_DISABLED),
		channelReply(6, pb.Channel_DISABLED),
		channelReply(7, pb.Channel_DISABLED),
	}
	got, err := s.chooseStagingSlot(context.Background(), 0, nil)
	if err != nil {
		t.Fatalf("chooseStagingSlot: %v", err)
	}
	if got != 0 {
		t.Errorf("PRIMARY at slot 1 -> staging picked %d, want 0 (lowest empty != PRIMARY)", got)
	}
}

// TestChooseStagingSlot_SkipsSlotZeroWhenItIsPrimary covers the
// reverse case from a fresh fleet: PRIMARY at slot 0 -> staging
// should pick slot 1.
func TestChooseStagingSlot_SkipsSlotZeroWhenItIsPrimary(t *testing.T) {
	s, _, fs := makeTestService(t)
	fs.replyQueue = []Reply{
		channelReply(0, pb.Channel_PRIMARY),
		channelReply(1, pb.Channel_DISABLED),
		channelReply(2, pb.Channel_DISABLED),
		channelReply(3, pb.Channel_DISABLED),
		channelReply(4, pb.Channel_DISABLED),
		channelReply(5, pb.Channel_DISABLED),
		channelReply(6, pb.Channel_DISABLED),
		channelReply(7, pb.Channel_DISABLED),
	}
	got, err := s.chooseStagingSlot(context.Background(), 0, nil)
	if err != nil {
		t.Fatalf("chooseStagingSlot: %v", err)
	}
	if got != 1 {
		t.Errorf("PRIMARY at slot 0 -> staging picked %d, want 1", got)
	}
}

// TestChooseStagingSlot_NoPrimary is a safety check: if no slot
// reports PRIMARY, abort with an error rather than picking a random
// slot. A live Heltec without PRIMARY can't TX/RX, so any rotation
// against it is doomed -- better to surface the broken state to the
// operator than to silently make it worse.
func TestChooseStagingSlot_NoPrimary(t *testing.T) {
	s, _, fs := makeTestService(t)
	for i := int32(0); i < 8; i++ {
		fs.replyQueue = append(fs.replyQueue, channelReply(i, pb.Channel_DISABLED))
	}
	if _, err := s.chooseStagingSlot(context.Background(), 0, nil); err == nil {
		t.Fatal("expected error when no PRIMARY slot found, got nil")
	}
}

// TestChooseStagingSlot_CrossFleetSkipsRemoteCollision covers the
// §10.9 follow-up bug: even with the local-only picker fix, the
// chosen slot could still be live on a remote (out-of-sync from a
// prior partial rotation or USB-side intervention). The cross-fleet
// probe must skip slot 0 if a targeted remote reports it as PRIMARY,
// even though it's DISABLED on the local Heltec.
//
// Scenario: Pi has PRIMARY at slot 1, slot 0 DISABLED. The remote
// has PRIMARY at slot 0 (skewed). Without the cross-fleet probe the
// picker would return slot 0, then Phase B would brick the remote.
// With the probe, the picker walks past slot 0 (live on remote) and
// returns slot 2 (lowest empty everywhere).
func TestChooseStagingSlot_CrossFleetSkipsRemoteCollision(t *testing.T) {
	s, _, fs := makeTestService(t)
	const remote uint32 = 0xa1b2c3d4
	fs.replyQueue = []Reply{
		// 8 local probes: PRIMARY at slot 1, all others DISABLED.
		channelReply(0, pb.Channel_DISABLED),
		channelReply(1, pb.Channel_PRIMARY),
		channelReply(2, pb.Channel_DISABLED),
		channelReply(3, pb.Channel_DISABLED),
		channelReply(4, pb.Channel_DISABLED),
		channelReply(5, pb.Channel_DISABLED),
		channelReply(6, pb.Channel_DISABLED),
		channelReply(7, pb.Channel_DISABLED),
		// Cost-aware remote probe: ONE slot per candidate.
		// Walk: slot 1 = local PRIMARY (skip, no remote probe).
		// Walk: slot 0 -> probe remote slot 0 -> PRIMARY (reject).
		channelReply(0, pb.Channel_PRIMARY),
		// Walk: slot 2 -> probe remote slot 2 -> DISABLED (accept).
		channelReply(2, pb.Channel_DISABLED),
	}
	got, err := s.chooseStagingSlot(context.Background(), 0, []uint32{remote})
	if err != nil {
		t.Fatalf("chooseStagingSlot: %v", err)
	}
	// Slot 0 empty locally but PRIMARY on remote -> rejected.
	// Slot 1 is local PRIMARY -> skipped without remote probe.
	// Slot 2 DISABLED everywhere -> chosen.
	if got != 2 {
		t.Errorf("staging slot = %d, want 2 (slot 0 collides with remote PRIMARY, slot 1 is local PRIMARY)", got)
	}
	// 10 outbound: 8 local probes + 2 remote probes (slot 0 reject + slot 2 accept).
	if len(fs.history) != 10 {
		t.Errorf("history len = %d, want 10 (8 local + 2 per-candidate remote probes)", len(fs.history))
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

func TestPushStagingToRemote_SendsGetChannelThenSetSecondary(t *testing.T) {
	s, _, fs := makeTestService(t)
	// Probe returns role=DISABLED (slot empty on remote, safe to write).
	// Then SetChannel routes back as a routing ack.
	const remote uint32 = 0xa1b2c3d4
	wantPasskey := []byte{0x42}
	const stagingIdx int32 = 1
	fs.replyQueue = []Reply{
		{Kind: ReplyAdminMsg, Admin: &pb.AdminMessage{
			SessionPasskey: wantPasskey,
			PayloadVariant: &pb.AdminMessage_GetChannelResponse{
				GetChannelResponse: &pb.Channel{Index: stagingIdx, Role: pb.Channel_DISABLED},
			},
		}},
		{Kind: ReplyRoutingAck, Routing: &pb.Routing{Variant: &pb.Routing_ErrorReason{ErrorReason: pb.Routing_NONE}}},
	}
	psk := []byte{0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f}

	if err := s.pushStagingToRemote(context.Background(), remote, stagingIdx, psk); err != nil {
		t.Fatalf("pushStagingToRemote: %v", err)
	}
	if len(fs.history) != 2 {
		t.Fatalf("history len = %d, want 2 (GetChannel probe + SetChannel)", len(fs.history))
	}
	first := captureAdmin(fs.history[0], t)
	second := captureAdmin(fs.history[1], t)
	if first.variant != "GetChannelRequest" || first.chanIdx != stagingIdx {
		t.Errorf("first admin = %+v, want GetChannelRequest(idx=%d)", first, stagingIdx)
	}
	if second.variant != "SetChannel" || second.chanIdx != stagingIdx || second.chanRole != pb.Channel_SECONDARY {
		t.Errorf("second admin = %+v, want SetChannel(idx=%d, SECONDARY)", second, stagingIdx)
	}
	if got := s.getSessionPasskey(remote); !bytesEq(got, wantPasskey) {
		t.Errorf("session passkey not cached: got %x want %x", got, wantPasskey)
	}
}

// channelReplyWithPsk is like channelReply but stamps a specific
// PSK byte so reconcile-tests can assert PSK is correctly carried
// through the SetChannel mirror.
func channelReplyWithPsk(idx int32, role pb.Channel_Role, psk byte) Reply {
	ch := &pb.Channel{
		Index:    idx,
		Role:     role,
		Settings: &pb.ChannelSettings{Psk: []byte{psk}},
	}
	return Reply{
		Kind: ReplyAdminMsg,
		Admin: &pb.AdminMessage{
			PayloadVariant: &pb.AdminMessage_GetChannelResponse{GetChannelResponse: ch},
		},
	}
}

// TestReconcileRemoteSlots_MirrorsSkewedRemote covers the Phase-0
// fleet-realign behavior. Scenario: Pi has PRIMARY at slot 1 + slot 0
// DISABLED (post-retire canonical layout). Remote has PRIMARY at
// slot 0 (skewed -- carryover from a botched prior rotation). Phase 0
// must rewrite remote slot 1 to PRIMARY (using firmware auto-demote
// to flip slot 0 to SECONDARY) THEN rewrite slot 0 to DISABLED.
// Final remote layout matches Pi.
func TestReconcileRemoteSlots_MirrorsSkewedRemote(t *testing.T) {
	s, _, fs := makeTestService(t)
	const remote uint32 = 0xa1b2c3d4
	const pskByte byte = 0xab
	fs.replyQueue = []Reply{
		// Local read slot 0: DISABLED (no settings)
		{Kind: ReplyAdminMsg, Admin: &pb.AdminMessage{
			PayloadVariant: &pb.AdminMessage_GetChannelResponse{
				GetChannelResponse: &pb.Channel{Index: 0, Role: pb.Channel_DISABLED},
			},
		}},
		// Local read slot 1: PRIMARY with pskByte
		channelReplyWithPsk(1, pb.Channel_PRIMARY, pskByte),
		// Remote read slot 0: PRIMARY with pskByte (skewed)
		channelReplyWithPsk(0, pb.Channel_PRIMARY, pskByte),
		// Remote read slot 1: DISABLED
		{Kind: ReplyAdminMsg, Admin: &pb.AdminMessage{
			PayloadVariant: &pb.AdminMessage_GetChannelResponse{
				GetChannelResponse: &pb.Channel{Index: 1, Role: pb.Channel_DISABLED},
			},
		}},
		// SetChannel(1, PRIMARY, psk) on remote -> routing ack
		{Kind: ReplyRoutingAck, Routing: &pb.Routing{Variant: &pb.Routing_ErrorReason{ErrorReason: pb.Routing_NONE}}},
		// SetChannel(0, DISABLED) on remote -> routing ack
		{Kind: ReplyRoutingAck, Routing: &pb.Routing{Variant: &pb.Routing_ErrorReason{ErrorReason: pb.Routing_NONE}}},
	}
	if err := s.reconcileRemoteSlots(context.Background(), remote); err != nil {
		t.Fatalf("reconcileRemoteSlots: %v", err)
	}
	// Expect 6 outbound: 4 GetChannel reads + 2 SetChannel writes.
	if len(fs.history) != 6 {
		t.Fatalf("history len = %d, want 6 (4 reads + 2 writes)", len(fs.history))
	}
	// fs.history[4] = first write: SetChannel(1, PRIMARY, ...)
	w1 := captureAdmin(fs.history[4], t)
	if w1.variant != "SetChannel" || w1.chanIdx != 1 || w1.chanRole != pb.Channel_PRIMARY {
		t.Errorf("write 1 = %+v, want SetChannel(idx=1, PRIMARY)", w1)
	}
	// fs.history[5] = second write: SetChannel(0, DISABLED)
	w2 := captureAdmin(fs.history[5], t)
	if w2.variant != "SetChannel" || w2.chanIdx != 0 || w2.chanRole != pb.Channel_DISABLED {
		t.Errorf("write 2 = %+v, want SetChannel(idx=0, DISABLED)", w2)
	}
}

// TestReconcileRemoteSlots_NoOpWhenAligned covers the steady-state
// case: remote already matches Pi -> reconcile issues zero writes.
func TestReconcileRemoteSlots_NoOpWhenAligned(t *testing.T) {
	s, _, fs := makeTestService(t)
	const remote uint32 = 0xa1b2c3d4
	const pskByte byte = 0xab
	fs.replyQueue = []Reply{
		// Pi slot 0: PRIMARY pskByte
		channelReplyWithPsk(0, pb.Channel_PRIMARY, pskByte),
		// Pi slot 1: DISABLED
		{Kind: ReplyAdminMsg, Admin: &pb.AdminMessage{
			PayloadVariant: &pb.AdminMessage_GetChannelResponse{
				GetChannelResponse: &pb.Channel{Index: 1, Role: pb.Channel_DISABLED},
			},
		}},
		// Remote slot 0: PRIMARY pskByte (matches)
		channelReplyWithPsk(0, pb.Channel_PRIMARY, pskByte),
		// Remote slot 1: DISABLED (matches)
		{Kind: ReplyAdminMsg, Admin: &pb.AdminMessage{
			PayloadVariant: &pb.AdminMessage_GetChannelResponse{
				GetChannelResponse: &pb.Channel{Index: 1, Role: pb.Channel_DISABLED},
			},
		}},
	}
	if err := s.reconcileRemoteSlots(context.Background(), remote); err != nil {
		t.Fatalf("reconcileRemoteSlots: %v", err)
	}
	if len(fs.history) != 4 {
		t.Errorf("history len = %d, want 4 (only reads, no writes)", len(fs.history))
	}
	// Confirm none of the 4 outbound was a SetChannel.
	for i, h := range fs.history {
		if got := captureAdmin(h, t); got.variant == "SetChannel" {
			t.Errorf("unexpected SetChannel at history[%d]: %+v", i, got)
		}
	}
}

// TestPushStagingToRemote_AbortsWhenSlotInUse covers the cross-fleet
// safety net: if the remote reports the staging slot is already
// PRIMARY (or SECONDARY), Phase B MUST refuse to write rather than
// overwriting an active channel. This is the failure mode that
// stranded HB35 in §10.9 -- the Pi-local picker chose slot 1 thinking
// it was empty, but on the remote slot 1 was the live PRIMARY. Phase B
// blasted SetChannel(1, SECONDARY, newPSK) and bricked the radio.
func TestPushStagingToRemote_AbortsWhenSlotInUse(t *testing.T) {
	s, _, fs := makeTestService(t)
	const remote uint32 = 0xa1b2c3d4
	const stagingIdx int32 = 1
	fs.replyQueue = []Reply{
		{Kind: ReplyAdminMsg, Admin: &pb.AdminMessage{
			SessionPasskey: []byte{0x77},
			PayloadVariant: &pb.AdminMessage_GetChannelResponse{
				GetChannelResponse: &pb.Channel{
					Index: stagingIdx,
					Role:  pb.Channel_PRIMARY,
					Settings: &pb.ChannelSettings{Psk: []byte{0xaa}},
				},
			},
		}},
	}
	psk := make([]byte, 16)
	err := s.pushStagingToRemote(context.Background(), remote, stagingIdx, psk)
	if err == nil {
		t.Fatal("expected error when remote slot is non-DISABLED, got nil")
	}
	if !errorContains(err, "already in use") {
		t.Errorf("error = %v, want 'already in use' guard", err)
	}
	if len(fs.history) != 1 {
		t.Errorf("history len = %d, want 1 (only the probe; SetChannel must NOT have been sent)", len(fs.history))
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
