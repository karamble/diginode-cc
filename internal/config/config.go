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

	// Database
	DatabaseURL string

	// Serial (Meshtastic)
	SerialDevice string
	SerialBaud   int

	// MQTT
	MQTTBrokerURL string
	MQTTEnabled   bool

	// ADS-B
	ADSBEnabled bool
	ADSBURL     string

	// ACARS
	ACARSEnabled bool
	ACARSPort    int

	// TAK
	TAKEnabled bool
	TAKAddr    string

	// Mail
	SMTPHost     string
	SMTPPort     int
	SMTPUser     string
	SMTPPassword string
	SMTPFrom     string

	// Firewall
	GeoIPDBPath string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() (*Config, error) {
	cfg := &Config{
		ListenAddr:   envOrDefault("LISTEN_ADDR", ":3000"),
		JWTSecret:    envOrDefault("JWT_SECRET", ""),
		DatabaseURL:  envOrDefault("DATABASE_URL", "postgres://diginode:diginode@localhost:5432/diginode?sslmode=disable"),
		SerialDevice: envOrDefault("SERIAL_DEVICE", ""),
		SerialBaud:   envOrDefaultInt("SERIAL_BAUD", 115200),
		MQTTEnabled:  envOrDefaultBool("MQTT_ENABLED", false),
		MQTTBrokerURL: envOrDefault("MQTT_BROKER_URL", "tcp://localhost:1883"),
		ADSBEnabled:  envOrDefaultBool("ADSB_ENABLED", false),
		ADSBURL:      envOrDefault("ADSB_URL", "http://localhost:8080/data/aircraft.json"),
		ACARSEnabled: envOrDefaultBool("ACARS_ENABLED", false),
		ACARSPort:    envOrDefaultInt("ACARS_PORT", 5555),
		TAKEnabled:   envOrDefaultBool("TAK_ENABLED", false),
		TAKAddr:      envOrDefault("TAK_ADDR", ""),
		SMTPHost:     envOrDefault("SMTP_HOST", ""),
		SMTPPort:     envOrDefaultInt("SMTP_PORT", 587),
		SMTPUser:     envOrDefault("SMTP_USER", ""),
		SMTPPassword: envOrDefault("SMTP_PASSWORD", ""),
		SMTPFrom:     envOrDefault("SMTP_FROM", ""),
		GeoIPDBPath:  envOrDefault("GEOIP_DB_PATH", ""),
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
