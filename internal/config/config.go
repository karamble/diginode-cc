package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all application configuration.
type Config struct {
	// Server
	ListenAddr string
	JWTSecret  string
	LogLevel   string

	// Database
	DatabaseURL string

	// Auth
	JWTExpiry                 string
	AuthLockoutThreshold      int
	AuthLockoutDurationMinutes int
	InviteExpiryHours         int
	PasswordResetExpiryHours  int
	TwoFactorIssuer           string
	AuthMinSubmitMS           int

	// Rate Limiting
	RateLimitDefault    int
	RateLimitDefaultTTL int
	RateLimitLogin      int
	RateLimitLoginTTL   int
	RateLimit2FA        int
	RateLimit2FATTL     int

	// WebSocket
	WSMaxClients int

	// Serial (Meshtastic)
	SerialDevice         string
	SerialBaud           int
	SerialReconnectBaseMS int
	SerialReconnectMaxMS  int
	SerialReconnectJitter float64

	// MQTT
	MQTTBrokerURL        string
	MQTTEnabled          bool
	MQTTConnectTimeoutMS int

	// ADS-B
	ADSBEnabled              bool
	ADSBURL                  string
	ADSBPollIntervalMS       int
	ADSBOpenSkyEnabled       bool
	ADSBOpenSkyClientID      string
	ADSBOpenSkyClientSecret  string
	ADSBPlanespottersEnabled bool

	// ACARS
	ACARSEnabled bool
	ACARSPort    int
	ACARSHost    string

	// TAK
	TAKEnabled  bool
	TAKAddr     string
	TAKProtocol string // "tcp" or "udp"
	TAKTLS      bool
	TAKUsername string
	TAKPassword string

	// FAA
	FAAOnlineLookupEnabled  bool
	FAACacheTTLMinutes      int

	// Mail
	MailEnabled  bool
	MailSecure   bool
	SMTPHost     string
	SMTPPort     int
	SMTPUser     string
	SMTPPassword string
	SMTPFrom     string

	// Tiles
	JawgAccessToken string

	// Updates
	AutoUpdateBranch string
	AutoUpdateRemote string

	// Firewall
	GeoIPDBPath string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() (*Config, error) {
	cfg := &Config{
		ListenAddr: envOrDefault("LISTEN_ADDR", ":3000"),
		JWTSecret:  envOrDefault("JWT_SECRET", ""),
		LogLevel:   envOrDefault("LOG_LEVEL", "info"),
		DatabaseURL: envOrDefault("DATABASE_URL", "postgres://diginode:diginode@localhost:5432/diginode?sslmode=disable"),

		// Auth
		JWTExpiry:                  envOrDefault("JWT_EXPIRY", "24h"),
		AuthLockoutThreshold:       envOrDefaultInt("AUTH_LOCKOUT_THRESHOLD", 4),
		AuthLockoutDurationMinutes: envOrDefaultInt("AUTH_LOCKOUT_DURATION_MINUTES", 15),
		InviteExpiryHours:          envOrDefaultInt("INVITE_EXPIRY_HOURS", 168),
		PasswordResetExpiryHours:   envOrDefaultInt("PASSWORD_RESET_EXPIRY_HOURS", 1),
		TwoFactorIssuer:            envOrDefault("TWO_FACTOR_ISSUER", "DigiNode CC"),
		AuthMinSubmitMS:            envOrDefaultInt("AUTH_MIN_SUBMIT_MS", 600),

		// Rate Limiting
		RateLimitDefault:    envOrDefaultInt("RATE_LIMIT_DEFAULT_LIMIT", 300),
		RateLimitDefaultTTL: envOrDefaultInt("RATE_LIMIT_DEFAULT_TTL", 60),
		RateLimitLogin:      envOrDefaultInt("RATE_LIMIT_LOGIN_LIMIT", 30),
		RateLimitLoginTTL:   envOrDefaultInt("RATE_LIMIT_LOGIN_TTL", 60),
		RateLimit2FA:        envOrDefaultInt("RATE_LIMIT_2FA_LIMIT", 10),
		RateLimit2FATTL:     envOrDefaultInt("RATE_LIMIT_2FA_TTL", 300),

		// WebSocket
		WSMaxClients: envOrDefaultInt("WS_MAX_CLIENTS", 200),

		// Serial
		SerialDevice:          envOrDefault("SERIAL_DEVICE", "/dev/lora"),
		SerialBaud:            envOrDefaultInt("SERIAL_BAUD", 115200),
		SerialReconnectBaseMS: envOrDefaultInt("SERIAL_RECONNECT_BASE_MS", 500),
		SerialReconnectMaxMS:  envOrDefaultInt("SERIAL_RECONNECT_MAX_MS", 15000),
		SerialReconnectJitter: envOrDefaultFloat("SERIAL_RECONNECT_JITTER", 0.2),

		// MQTT
		MQTTEnabled:          envOrDefaultBool("MQTT_ENABLED", false),
		MQTTBrokerURL:        envOrDefault("MQTT_BROKER_URL", "tcp://localhost:1883"),
		MQTTConnectTimeoutMS: envOrDefaultInt("MQTT_CONNECT_TIMEOUT_MS", 5000),

		// ADS-B
		ADSBEnabled:        envOrDefaultBool("ADSB_ENABLED", false),
		ADSBURL:            envOrDefault("ADSB_URL", "http://localhost:8080/data/aircraft.json"),
		ADSBPollIntervalMS:       envOrDefaultInt("ADSB_POLL_INTERVAL_MS", 3000),
		ADSBOpenSkyEnabled:       envOrDefaultBool("ADSB_OPENSKY_ENABLED", false),
		ADSBOpenSkyClientID:      envOrDefault("ADSB_OPENSKY_CLIENT_ID", ""),
		ADSBOpenSkyClientSecret:  envOrDefault("ADSB_OPENSKY_CLIENT_SECRET", ""),
		ADSBPlanespottersEnabled: envOrDefaultBool("ADSB_PLANESPOTTERS_ENABLED", true),

		// ACARS
		ACARSEnabled: envOrDefaultBool("ACARS_ENABLED", false),
		ACARSPort:    envOrDefaultInt("ACARS_PORT", 5555),
		ACARSHost:    envOrDefault("ACARS_UDP_HOST", "0.0.0.0"),

		// TAK
		TAKEnabled:  envOrDefaultBool("TAK_ENABLED", false),
		TAKAddr:     envOrDefault("TAK_ADDR", ""),
		TAKProtocol: envOrDefault("TAK_PROTOCOL", "tcp"),
		TAKTLS:      envOrDefaultBool("TAK_TLS", false),
		TAKUsername: envOrDefault("TAK_USERNAME", ""),
		TAKPassword: envOrDefault("TAK_PASSWORD", ""),

		// FAA
		FAAOnlineLookupEnabled: envOrDefaultBool("FAA_ONLINE_LOOKUP_ENABLED", true),
		FAACacheTTLMinutes:     envOrDefaultInt("FAA_ONLINE_CACHE_TTL_MINUTES", 10),

		// Mail
		MailEnabled:  envOrDefaultBool("MAIL_ENABLED", false),
		MailSecure:   envOrDefaultBool("MAIL_SECURE", false),
		SMTPHost:     envOrDefault("SMTP_HOST", ""),
		SMTPPort:     envOrDefaultInt("SMTP_PORT", 587),
		SMTPUser:     envOrDefault("SMTP_USER", ""),
		SMTPPassword: envOrDefault("SMTP_PASSWORD", ""),
		SMTPFrom:     envOrDefault("SMTP_FROM", ""),

		// Tiles
		JawgAccessToken: envOrDefault("JAWG_ACCESS_TOKEN", ""),

		// Updates
		AutoUpdateBranch: envOrDefault("AUTO_UPDATE_BRANCH", "master"),
		AutoUpdateRemote: envOrDefault("AUTO_UPDATE_REMOTE", "origin"),

		// Firewall
		GeoIPDBPath: envOrDefault("GEOIP_DB_PATH", ""),
	}

	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET environment variable is required")
	}

	return cfg, nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envOrDefaultInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

func envOrDefaultBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func envOrDefaultFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}
