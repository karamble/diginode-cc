// Package probes tracks WiFi probe-request observations grouped by SSID +
// detecting node, since modern devices randomize MAC on every probe and the
// SSID is the stable identity that reveals location-history overlap across
// a sensor mesh. Backed by the probe_ssids and probe_ssid_mac_samples tables.
package probes

import (
	"context"
	"database/sql"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
)

// SSIDStat is one row of probe_ssids — the per-(ssid, node) summary.
type SSIDStat struct {
	SSID           string    `json:"ssid"`
	NodeID         string    `json:"nodeId"`
	FirstSeen      time.Time `json:"firstSeen"`
	LastSeen       time.Time `json:"lastSeen"`
	HitCount       int       `json:"hitCount"`
	GhostCount     int       `json:"ghostCount"`
	RespondedCount int       `json:"respondedCount"`
	DstCount       int       `json:"dstCount"`
	LastRSSI       int       `json:"lastRssi,omitempty"`
	LastChannel    int       `json:"lastChannel,omitempty"`
	LastMAC        string    `json:"lastMac,omitempty"`
	// DistinctMacs24h is populated by query helpers that join probe_ssid_mac_samples,
	// not by the base SELECT. Zero when not requested.
	DistinctMacs24h int `json:"distinctMacs24h,omitempty"`
}

// Service is the entry point. Track() is the hot path — called from the
// alert pipeline whenever a PROBE_HIT is parsed. Persistence is async
// (per-call goroutine) so the parse loop never blocks on a DB write.
type Service struct {
	db *database.DB
	mu sync.Mutex
}

func NewService(db *database.DB) *Service {
	return &Service{db: db}
}

// Track records one probe observation. ssid="" silently skips — a probe with
// no SSID IE has no useful identity to pivot on. ghost and dst are mutually
// non-exclusive flags from the firmware. nodeID and mac are upper-cased and
// trimmed; we don't validate format here, the parser is upstream.
func (s *Service) Track(ssid, nodeID, mac string, rssi, channel int, ghost, dst bool) {
	ssid = strings.TrimSpace(ssid)
	nodeID = strings.TrimSpace(nodeID)
	if ssid == "" || nodeID == "" {
		return
	}
	mac = strings.ToUpper(strings.TrimSpace(mac))

	go s.persist(ssid, nodeID, mac, rssi, channel, ghost, dst)
}

func (s *Service) persist(ssid, nodeID, mac string, rssi, channel int, ghost, dst bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ghostInc := 0
	if ghost {
		ghostInc = 1
	}
	respondedInc := 0
	if !ghost {
		// Firmware only suppresses GHOST when it has seen a probe response for
		// this SSID at this sensor — treat absence-of-ghost as confirmation.
		respondedInc = 1
	}
	dstInc := 0
	if dst {
		dstInc = 1
	}

	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO probe_ssids (ssid, node_id, first_seen, last_seen,
			hit_count, ghost_count, responded_count, dst_count,
			last_rssi, last_channel, last_mac)
		VALUES ($1, $2, NOW(), NOW(), 1, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (ssid, node_id) DO UPDATE SET
			last_seen        = NOW(),
			hit_count        = probe_ssids.hit_count + 1,
			ghost_count      = probe_ssids.ghost_count + EXCLUDED.ghost_count,
			responded_count  = probe_ssids.responded_count + EXCLUDED.responded_count,
			dst_count        = probe_ssids.dst_count + EXCLUDED.dst_count,
			last_rssi        = EXCLUDED.last_rssi,
			last_channel     = COALESCE(EXCLUDED.last_channel, probe_ssids.last_channel),
			last_mac         = COALESCE(EXCLUDED.last_mac, probe_ssids.last_mac)`,
		ssid, nodeID, ghostInc, respondedInc, dstInc,
		nilIfZeroInt(rssi), nilIfZeroInt(channel), nilIfEmpty(mac),
	)
	if err != nil {
		slog.Error("probes: failed to upsert probe_ssids", "ssid", ssid, "node", nodeID, "error", err)
		return
	}

	// MAC sample row — only when we actually have a MAC. The randomized MAC
	// is itself the count: distinct MACs probing for this SSID at this node
	// in 24h is the "how many devices" estimate.
	if mac != "" {
		_, err = s.db.Pool.Exec(ctx, `
			INSERT INTO probe_ssid_mac_samples (ssid, node_id, mac, last_seen)
			VALUES ($1, $2, $3, NOW())
			ON CONFLICT (ssid, node_id, mac) DO UPDATE SET last_seen = NOW()`,
			ssid, nodeID, mac)
		if err != nil {
			slog.Warn("probes: failed to upsert mac sample", "ssid", ssid, "node", nodeID, "mac", mac, "error", err)
		}
	}
}

// PruneMacSamples removes mac samples older than the given retention window.
// Caller schedules this on a timer — recommend hourly with retention=24h.
// Returns the number of rows pruned.
func (s *Service) PruneMacSamples(ctx context.Context, retention time.Duration) (int64, error) {
	tag, err := s.db.Pool.Exec(ctx, `
		DELETE FROM probe_ssid_mac_samples WHERE last_seen < NOW() - $1::interval`,
		retention.String())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ListAll returns probe_ssids rows ordered by last_seen DESC. limit=0 means
// no limit. Each row is enriched with distinct_macs_24h via a correlated
// subquery against probe_ssid_mac_samples.
func (s *Service) ListAll(ctx context.Context, limit int) ([]SSIDStat, error) {
	q := `
		SELECT p.ssid, p.node_id, p.first_seen, p.last_seen,
			p.hit_count, p.ghost_count, p.responded_count, p.dst_count,
			p.last_rssi, p.last_channel, p.last_mac,
			(SELECT COUNT(*) FROM probe_ssid_mac_samples m
			   WHERE m.ssid = p.ssid AND m.node_id = p.node_id
			     AND m.last_seen > NOW() - INTERVAL '24 hours') AS distinct_macs_24h
		FROM probe_ssids p
		ORDER BY p.last_seen DESC`
	if limit > 0 {
		q += " LIMIT $1"
		return s.scanRows(ctx, q, limit)
	}
	return s.scanRows(ctx, q)
}

// GetForSSID returns all rows for a given SSID across all nodes — useful for
// "which sensors have seen this network probed for?". Results are ordered
// node_id ASC.
func (s *Service) GetForSSID(ctx context.Context, ssid string) ([]SSIDStat, error) {
	q := `
		SELECT p.ssid, p.node_id, p.first_seen, p.last_seen,
			p.hit_count, p.ghost_count, p.responded_count, p.dst_count,
			p.last_rssi, p.last_channel, p.last_mac,
			(SELECT COUNT(*) FROM probe_ssid_mac_samples m
			   WHERE m.ssid = p.ssid AND m.node_id = p.node_id
			     AND m.last_seen > NOW() - INTERVAL '24 hours') AS distinct_macs_24h
		FROM probe_ssids p
		WHERE p.ssid = $1
		ORDER BY p.node_id ASC`
	return s.scanRows(ctx, q, ssid)
}

// GetForCommandWindow returns probe_ssids rows last_seen within the given
// [after, before] window, optionally filtered to a single detecting node.
// Used by the command-details modal so each PROBE_START shows the SSIDs
// captured during its specific scan window. nodeID="" means any node;
// zero-value times mean "unbounded" on that side.
func (s *Service) GetForCommandWindow(ctx context.Context, nodeID string, after, before time.Time) ([]SSIDStat, error) {
	q := `
		SELECT p.ssid, p.node_id, p.first_seen, p.last_seen,
			p.hit_count, p.ghost_count, p.responded_count, p.dst_count,
			p.last_rssi, p.last_channel, p.last_mac,
			(SELECT COUNT(*) FROM probe_ssid_mac_samples m
			   WHERE m.ssid = p.ssid AND m.node_id = p.node_id
			     AND m.last_seen > NOW() - INTERVAL '24 hours') AS distinct_macs_24h
		FROM probe_ssids p
		WHERE TRUE`
	args := []interface{}{}
	if nodeID != "" {
		args = append(args, nodeID)
		q += " AND p.node_id = $" + strconv.Itoa(len(args))
	}
	if !after.IsZero() {
		args = append(args, after)
		q += " AND p.last_seen >= $" + strconv.Itoa(len(args))
	}
	if !before.IsZero() {
		args = append(args, before)
		q += " AND p.last_seen <= $" + strconv.Itoa(len(args))
	}
	q += " ORDER BY p.hit_count DESC, p.last_seen DESC"
	return s.scanRows(ctx, q, args...)
}

// GetForNode returns all rows for a given detecting node — "what SSIDs has
// AH01 seen probed for?". Ordered hit_count DESC.
func (s *Service) GetForNode(ctx context.Context, nodeID string) ([]SSIDStat, error) {
	q := `
		SELECT p.ssid, p.node_id, p.first_seen, p.last_seen,
			p.hit_count, p.ghost_count, p.responded_count, p.dst_count,
			p.last_rssi, p.last_channel, p.last_mac,
			(SELECT COUNT(*) FROM probe_ssid_mac_samples m
			   WHERE m.ssid = p.ssid AND m.node_id = p.node_id
			     AND m.last_seen > NOW() - INTERVAL '24 hours') AS distinct_macs_24h
		FROM probe_ssids p
		WHERE p.node_id = $1
		ORDER BY p.hit_count DESC`
	return s.scanRows(ctx, q, nodeID)
}

func (s *Service) scanRows(ctx context.Context, q string, args ...interface{}) ([]SSIDStat, error) {
	rows, err := s.db.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SSIDStat
	for rows.Next() {
		var r SSIDStat
		var rssi, channel sql.NullInt32
		var mac sql.NullString
		if err := rows.Scan(
			&r.SSID, &r.NodeID, &r.FirstSeen, &r.LastSeen,
			&r.HitCount, &r.GhostCount, &r.RespondedCount, &r.DstCount,
			&rssi, &channel, &mac, &r.DistinctMacs24h,
		); err != nil {
			return nil, err
		}
		if rssi.Valid {
			r.LastRSSI = int(rssi.Int32)
		}
		if channel.Valid {
			r.LastChannel = int(channel.Int32)
		}
		if mac.Valid {
			r.LastMAC = mac.String
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func nilIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nilIfZeroInt(n int) interface{} {
	if n == 0 {
		return nil
	}
	return n
}
