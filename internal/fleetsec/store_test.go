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
