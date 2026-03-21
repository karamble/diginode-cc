package config

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AppConfig manages runtime application configuration stored in the database.
// This is separate from Config which handles environment/startup config.
type AppConfig struct {
	pool   *pgxpool.Pool
	cache  map[string]json.RawMessage
	mu     sync.RWMutex
}

// NewAppConfig creates a new application config manager.
func NewAppConfig(pool *pgxpool.Pool) *AppConfig {
	return &AppConfig{
		pool:  pool,
		cache: make(map[string]json.RawMessage),
	}
}

// Load reads all config values from the database.
func (ac *AppConfig) Load(ctx context.Context) error {
	rows, err := ac.pool.Query(ctx, `SELECT key, value FROM app_config WHERE site_id IS NULL`)
	if err != nil {
		return err
	}
	defer rows.Close()

	ac.mu.Lock()
	defer ac.mu.Unlock()

	for rows.Next() {
		var key string
		var value json.RawMessage
		if err := rows.Scan(&key, &value); err != nil {
			continue
		}
		ac.cache[key] = value
	}
	return nil
}

// Get returns a config value by key.
func (ac *AppConfig) Get(key string) (json.RawMessage, bool) {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	val, ok := ac.cache[key]
	return val, ok
}

// GetTyped unmarshals a config value into the given target.
func (ac *AppConfig) GetTyped(key string, target interface{}) error {
	ac.mu.RLock()
	val, ok := ac.cache[key]
	ac.mu.RUnlock()

	if !ok {
		return nil
	}
	return json.Unmarshal(val, target)
}

// Set stores a config value.
func (ac *AppConfig) Set(ctx context.Context, key string, value interface{}) error {
	jsonVal, err := json.Marshal(value)
	if err != nil {
		return err
	}

	_, err = ac.pool.Exec(ctx, `
		INSERT INTO app_config (key, value, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (site_id, key) WHERE site_id IS NULL
		DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
		key, jsonVal,
	)
	if err != nil {
		return err
	}

	ac.mu.Lock()
	ac.cache[key] = jsonVal
	ac.mu.Unlock()

	return nil
}

// EnsureDefaults creates all required config keys with default values if they don't exist.
func (ac *AppConfig) EnsureDefaults(ctx context.Context) error {
	defaults := map[string]interface{}{
		"appName":                  "DigiNode CC",
		"timezone":                 "",
		"env":                      "PRODUCTION",
		"protocol":                 "meshtastic-binary",
		"ackTimeoutMs":             3000,
		"resultTimeoutMs":          10000,
		"maxRetries":               2,
		"perNodeCmdRate":           8,
		"globalCmdRate":            30,
		"detectMode":               2,
		"detectChannels":           "1..14",
		"detectScanSecs":           300,
		"allowForever":             false,
		"baselineSecs":             300,
		"deviceScanSecs":           300,
		"droneSecs":                600,
		"deauthSecs":               300,
		"randomizeSecs":            600,
		"defaultRadiusM":           50,
		"logLevel":                 "info",
		"structuredLogs":           true,
		"nodePosRetentionDays":     30,
		"commandRetentionDays":     180,
		"auditRetentionDays":       365,
		"metricsEnabled":           false,
		"metricsPath":              "/metrics",
		"healthEnabled":            true,
		"mapTileUrl":               "https://tile.openstreetmap.org/{z}/{x}/{y}.png",
		"mapAttribution":           "OpenStreetMap",
		"minZoom":                  2,
		"maxZoom":                  18,
		"invitationExpiryHours":    48,
		"passwordResetExpiryHours": 4,
	}

	for key, val := range defaults {
		if _, exists := ac.Get(key); !exists {
			if err := ac.Set(ctx, key, val); err != nil {
				return err
			}
		}
	}
	return nil
}

// GetAll returns all config entries.
func (ac *AppConfig) GetAll() map[string]json.RawMessage {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	result := make(map[string]json.RawMessage, len(ac.cache))
	for k, v := range ac.cache {
		result[k] = v
	}
	return result
}
