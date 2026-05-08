package fleetsec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
)

// requireDB is the gate for the integration tests in this file. Skips
// unless FLEETSEC_TEST_DB is set to a Postgres URL of a database where
// the diginode migrations (000001..000024) have already been applied.
//
// Quick local setup:
//
//	docker run --rm -d --name digi-test-pg -e POSTGRES_PASSWORD=test \
//	  -e POSTGRES_DB=test -p 54330:5432 postgres:16-alpine
//	# wait for ready, then run migrations via diginode-cc once
//	export FLEETSEC_TEST_DB=postgres://postgres:test@localhost:54330/test?sslmode=disable
//	go test ./internal/fleetsec/ -run TestStore -v
func requireDB(t *testing.T) *database.DB {
	t.Helper()
	url := os.Getenv("FLEETSEC_TEST_DB")
	if url == "" {
		t.Skip("FLEETSEC_TEST_DB not set; skipping store integration test")
	}
	db, err := database.New(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

// resetFleetTables truncates only the fleet_* tables so each subtest starts
// clean. fleet_node_trust depends on a node row, so we also create one.
func resetFleetTables(ctx context.Context, t *testing.T, db *database.DB) {
	t.Helper()
	_, err := db.Pool.Exec(ctx, `
		TRUNCATE fleet_rotations, fleet_channels, fleet_node_trust,
		         fleet_identities RESTART IDENTITY CASCADE;
		UPDATE fleet_policy
		   SET expected_admin_key_fps = '[]'::jsonb,
		       expected_is_managed    = false,
		       expected_channels      = '[]'::jsonb,
		       updated_by             = NULL
		 WHERE id = 1;`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func makePubkey(seed byte) (pub []byte, fp string) {
	pub = make([]byte, 32)
	for i := range pub {
		pub[i] = seed
	}
	h := sha256.Sum256(pub)
	parts := make([]string, 8)
	for i := 0; i < 8; i++ {
		parts[i] = hex.EncodeToString(h[i : i+1])
	}
	fp = strings.Join(parts, ":")
	return
}

func TestStore_IdentityRoundTrip(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	resetFleetTables(ctx, t, db)
	s := NewStore(db)

	pub, fp := makePubkey(0xab)
	id, err := s.InsertIdentity(ctx, IdentityRecord{
		Label:       "cc-primary",
		PublicKey:   pub,
		Fingerprint: fp,
		Role:        IdentityRolePrimary,
		Source:      IdentitySourceImported,
		Notes:       "test",
	})
	if err != nil {
		t.Fatalf("InsertIdentity: %v", err)
	}
	if id == "" {
		t.Fatal("InsertIdentity returned empty id")
	}

	got, err := s.GetIdentityByFingerprint(ctx, fp)
	if err != nil {
		t.Fatalf("GetIdentityByFingerprint: %v", err)
	}
	if got.Label != "cc-primary" || got.Role != IdentityRolePrimary {
		t.Errorf("got %+v", got)
	}
	if string(got.PublicKey) != string(pub) {
		t.Error("pubkey mismatch")
	}

	if err := s.RevokeIdentity(ctx, fp, "test compromise"); err != nil {
		t.Fatalf("RevokeIdentity: %v", err)
	}
	got, err = s.GetIdentityByFingerprint(ctx, fp)
	if err != nil {
		t.Fatalf("get after revoke: %v", err)
	}
	if got.Role != IdentityRoleRevoked {
		t.Errorf("after revoke role = %v, want revoked", got.Role)
	}
	if got.RevokedAt == nil {
		t.Error("RevokedAt should be populated")
	}

	all, err := s.ListIdentities(ctx)
	if err != nil {
		t.Fatalf("ListIdentities: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("ListIdentities returned %d, want 1", len(all))
	}
}

// makeTestNode inserts a row into nodes so fleet_node_trust's FK target
// exists. Returns the node_num to use for trust ops.
func makeTestNode(ctx context.Context, t *testing.T, db *database.DB, num int64) {
	t.Helper()
	_, err := db.Pool.Exec(ctx, `
		INSERT INTO nodes (node_num, node_id, long_name, short_name)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (node_num) DO NOTHING`,
		num, fmt.Sprintf("!%08x", num), "Test Node", "TST")
	if err != nil {
		t.Fatalf("seed node: %v", err)
	}
}

func TestStore_NodeTrustRoundTrip(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	resetFleetTables(ctx, t, db)
	s := NewStore(db)

	makeTestNode(ctx, t, db, 0xa1b2c3d4)

	// Before any upsert: list returns the node with default trust state.
	rows, err := s.ListNodeTrust(ctx)
	if err != nil {
		t.Fatalf("ListNodeTrust: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least one node row")
	}

	now := time.Now().UTC().Truncate(time.Second)
	_, fp1 := makePubkey(0x01)
	_, fp2 := makePubkey(0x02)
	err = s.UpsertNodeTrust(ctx, NodeTrustRecord{
		NodeNum:           0xa1b2c3d4,
		AdminKeyFPs:       []string{fp1, fp2},
		IsManaged:         false,
		LastVerifiedAt:    &now,
		LastVerifyMethod:  VerifyMethodRemotePKC,
		DriftStatus:       DriftStatusInPolicy,
	})
	if err != nil {
		t.Fatalf("UpsertNodeTrust: %v", err)
	}

	got, err := s.GetNodeTrust(ctx, 0xa1b2c3d4)
	if err != nil {
		t.Fatalf("GetNodeTrust: %v", err)
	}
	if len(got.AdminKeyFPs) != 2 || got.AdminKeyFPs[0] != fp1 || got.AdminKeyFPs[1] != fp2 {
		t.Errorf("admin_key_fps = %v", got.AdminKeyFPs)
	}
	if got.DriftStatus != DriftStatusInPolicy {
		t.Errorf("drift_status = %v", got.DriftStatus)
	}
	if got.LastVerifyMethod != VerifyMethodRemotePKC {
		t.Errorf("last_verify_method = %v", got.LastVerifyMethod)
	}
	if got.LastVerifiedAt == nil || !got.LastVerifiedAt.Equal(now) {
		t.Errorf("last_verified_at = %v, want %v", got.LastVerifiedAt, now)
	}
}

func TestStore_PolicyRoundTrip(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	resetFleetTables(ctx, t, db)
	s := NewStore(db)

	// Default policy after migration: empty lists, is_managed=false.
	p, err := s.GetPolicy(ctx)
	if err != nil {
		t.Fatalf("GetPolicy initial: %v", err)
	}
	if len(p.ExpectedAdminKeyFPs) != 0 || len(p.ExpectedChannels) != 0 || p.ExpectedIsManaged {
		t.Errorf("initial policy non-empty: %+v", p)
	}

	_, fp := makePubkey(0xff)
	updated := FleetPolicy{
		ExpectedAdminKeyFPs: []string{fp},
		ExpectedIsManaged:   true,
		ExpectedChannels: []ExpectedChannel{
			{Index: 0, Name: "halberd-data", Role: ChannelRolePrimary},
		},
	}
	if err := s.UpdatePolicy(ctx, updated, ""); err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}

	p, err = s.GetPolicy(ctx)
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}
	if len(p.ExpectedAdminKeyFPs) != 1 || p.ExpectedAdminKeyFPs[0] != fp {
		t.Errorf("expected_admin_key_fps = %v", p.ExpectedAdminKeyFPs)
	}
	if !p.ExpectedIsManaged {
		t.Error("expected_is_managed = false, want true")
	}
	if len(p.ExpectedChannels) != 1 || p.ExpectedChannels[0].Name != "halberd-data" {
		t.Errorf("expected_channels = %+v", p.ExpectedChannels)
	}
}

// TestStore_StagedRotationLifecycle covers the rotation-row methods
// added by migration 000027 + 000028. Insert a rotation, transition
// per-target phases, stamp pi_local_phase, mark retired, and confirm
// the AllManagedNodesOnPSK retirement gate flips false→true as nodes
// move to the new fingerprint.
//
// DB-gated like the other TestStore_* tests; skips without
// FLEETSEC_TEST_DB.
func TestStore_StagedRotationLifecycle(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	resetFleetTables(ctx, t, db)
	s := NewStore(db)

	const hb35 = uint32(0x0409cf64)
	const hb55 = uint32(0x02ed5f04)
	const local = uint32(0x0409c9d8)
	makeTestNode(ctx, t, db, int64(hb35))
	makeTestNode(ctx, t, db, int64(hb55))
	makeTestNode(ctx, t, db, int64(local))

	// Seed a managed-trust row for each remote (admin_key_fps populated
	// makes the row "managed" per AllManagedNodesOnPSK's filter).
	_, fp := makePubkey(0xab)
	for _, n := range []uint32{hb35, hb55} {
		if err := s.UpsertNodeTrust(ctx, NodeTrustRecord{
			NodeNum:     n,
			AdminKeyFPs: []string{fp},
			DriftStatus: DriftStatusInPolicy,
		}); err != nil {
			t.Fatalf("seed trust %x: %v", n, err)
		}
	}

	// Insert a fresh staged rotation.
	chanIdx := int32(0)
	pskRaw := []byte("0123456789abcdef")
	pskFP := Fingerprint(pskRaw)
	rotID, err := s.InsertRotation(ctx, RotationRecord{
		Kind:         RotationKindPSK,
		ChannelIndex: &chanIdx,
		Targets: []RotationTarget{
			{NodeNum: hb35, Phase: PhasePending, Status: TargetStatusPending},
			{NodeNum: hb55, Phase: PhasePending, Status: TargetStatusPending},
			{NodeNum: local, Phase: PhasePending, Status: TargetStatusPending},
		},
		NewPSKFP: pskFP,
		Notes:    "staged-rotation lifecycle test",
	}, pskRaw)
	if err != nil {
		t.Fatalf("InsertRotation: %v", err)
	}

	// Pin the staging slot.
	if err := s.SetStagingChannelIndex(ctx, rotID, 1); err != nil {
		t.Fatalf("SetStagingChannelIndex: %v", err)
	}

	// Pi finishes Phase A.
	if err := s.UpsertPiLocalPhase(ctx, rotID, PiPhaseStagingAdded); err != nil {
		t.Fatalf("UpsertPiLocalPhase=staging_added: %v", err)
	}

	// Walk HB35 through B → C → on_new_psk; HB55 fails Phase B.
	if err := s.IncrementTargetAttempts(ctx, rotID, hb35); err != nil {
		t.Fatalf("IncrementTargetAttempts hb35: %v", err)
	}
	if err := s.UpdateTargetPhase(ctx, rotID, hb35, PhasePushingB, ""); err != nil {
		t.Fatalf("UpdateTargetPhase hb35 -> pushing_b: %v", err)
	}
	if err := s.UpdateTargetPhase(ctx, rotID, hb35, PhaseHasNewPSK, ""); err != nil {
		t.Fatalf("UpdateTargetPhase hb35 -> has_new_psk: %v", err)
	}
	if err := s.UpdateTargetPhase(ctx, rotID, hb35, PhasePromotingC, ""); err != nil {
		t.Fatalf("UpdateTargetPhase hb35 -> promoting_c: %v", err)
	}
	if err := s.UpdateTargetPhase(ctx, rotID, hb35, PhaseOnNewPSK, ""); err != nil {
		t.Fatalf("UpdateTargetPhase hb35 -> on_new_psk: %v", err)
	}
	if err := s.IncrementTargetAttempts(ctx, rotID, hb55); err != nil {
		t.Fatalf("IncrementTargetAttempts hb55: %v", err)
	}
	if err := s.UpdateTargetPhase(ctx, rotID, hb55, PhaseFailedB, "session establish: no route"); err != nil {
		t.Fatalf("UpdateTargetPhase hb55 -> failed_b: %v", err)
	}
	// Local goes through promote phase too.
	if err := s.UpdateTargetPhase(ctx, rotID, local, PhaseOnNewPSK, ""); err != nil {
		t.Fatalf("UpdateTargetPhase local: %v", err)
	}

	// Stamp the per-node migration tracking for HB35 + local but NOT
	// HB55 (it failed Phase B). AllManagedNodesOnPSK should report
	// HB55 as a laggard.
	if err := s.SetNodeCurrentPSKFP(ctx, hb35, pskFP); err != nil {
		t.Fatalf("SetNodeCurrentPSKFP hb35: %v", err)
	}

	allMigrated, laggards, err := s.AllManagedNodesOnPSK(ctx, pskFP)
	if err != nil {
		t.Fatalf("AllManagedNodesOnPSK: %v", err)
	}
	if allMigrated {
		t.Error("expected gate to be CLOSED while HB55 still laggard")
	}
	if len(laggards) != 1 || laggards[0] != hb55 {
		t.Errorf("laggards = %v, want [hb55]", laggards)
	}

	// Pi finishes Phase D.
	if err := s.UpsertPiLocalPhase(ctx, rotID, PiPhasePhaseDPromoted); err != nil {
		t.Fatalf("UpsertPiLocalPhase=phase_d_promoted: %v", err)
	}

	// Verify GetRotation reads back the new shape correctly.
	got, err := s.GetRotation(ctx, rotID)
	if err != nil {
		t.Fatalf("GetRotation: %v", err)
	}
	if got.StagingChannelIndex == nil || *got.StagingChannelIndex != 1 {
		t.Errorf("StagingChannelIndex = %v, want 1", got.StagingChannelIndex)
	}
	if got.PiLocalPhase != PiPhasePhaseDPromoted {
		t.Errorf("PiLocalPhase = %s, want phase_d_promoted", got.PiLocalPhase)
	}
	if got.RetiredAt != nil {
		t.Errorf("RetiredAt should still be nil before retire; got %v", got.RetiredAt)
	}
	for _, target := range got.Targets {
		switch target.NodeNum {
		case hb35:
			if target.Phase != PhaseOnNewPSK {
				t.Errorf("hb35 phase = %s, want on_new_psk", target.Phase)
			}
			if target.Attempts != 1 {
				t.Errorf("hb35 attempts = %d, want 1", target.Attempts)
			}
		case hb55:
			if target.Phase != PhaseFailedB {
				t.Errorf("hb55 phase = %s, want failed_b", target.Phase)
			}
			if target.LastError == "" {
				t.Error("hb55 lastError should be populated")
			}
		case local:
			if target.Phase != PhaseOnNewPSK {
				t.Errorf("local phase = %s, want on_new_psk", target.Phase)
			}
		}
	}

	// Bring HB55 home: stamp its current_psk_fp, gate opens.
	if err := s.SetNodeCurrentPSKFP(ctx, hb55, pskFP); err != nil {
		t.Fatalf("SetNodeCurrentPSKFP hb55: %v", err)
	}
	allMigrated, laggards, err = s.AllManagedNodesOnPSK(ctx, pskFP)
	if err != nil {
		t.Fatalf("AllManagedNodesOnPSK after hb55: %v", err)
	}
	if !allMigrated {
		t.Errorf("expected gate to be OPEN once every node stamped; laggards=%v", laggards)
	}

	// Retire.
	if err := s.MarkRotationRetired(ctx, rotID); err != nil {
		t.Fatalf("MarkRotationRetired: %v", err)
	}
	got, err = s.GetRotation(ctx, rotID)
	if err != nil {
		t.Fatalf("GetRotation post-retire: %v", err)
	}
	if got.PiLocalPhase != PiPhaseRetired {
		t.Errorf("post-retire PiLocalPhase = %s, want retired", got.PiLocalPhase)
	}
	if got.RetiredAt == nil {
		t.Error("RetiredAt should be populated after retire")
	}

	// Confirm the trust list surfaces current_psk_fp for both remotes.
	listed, err := s.ListNodeTrust(ctx)
	if err != nil {
		t.Fatalf("ListNodeTrust: %v", err)
	}
	seen := map[uint32]string{}
	for _, r := range listed {
		seen[r.NodeNum] = r.CurrentPSKFP
	}
	if seen[hb35] != pskFP {
		t.Errorf("hb35 current_psk_fp = %q, want %q", seen[hb35], pskFP)
	}
	if seen[hb55] != pskFP {
		t.Errorf("hb55 current_psk_fp = %q, want %q", seen[hb55], pskFP)
	}
}
