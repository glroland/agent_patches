package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all runtime configuration for the gateway, loaded from
// environment variables so the same image can be tuned via Helm values.
type Config struct {
	ListenAddr  string
	UpstreamURL string
	// UpstreamModel is a display-only label for the model served at UpstreamURL,
	// surfaced via GET /stats. Purely informational — the gateway itself is
	// model-agnostic and does not inspect or alter request bodies for it.
	UpstreamModel      string
	MaxConcurrency     int
	MaxQueueDepth      int
	PriorityQueueDepth int
	RequestTimeout     time.Duration
	// AuthToken is the bearer token callers must supply in
	// Authorization: Bearer <token>. Empty string disables auth (insecure;
	// a warning is logged at startup).
	AuthToken string
	// DataFile is the path to the JSON file used to persist token/request
	// statistics across restarts. Empty string disables persistence.
	DataFile     string
	SaveInterval time.Duration
}

// LoadConfig reads configuration from environment variables, applying
// defaults for every optional field. Returns an error if any required
// field is absent or any value is out of range.
func LoadConfig() (Config, error) {
	cfg := Config{
		ListenAddr:         envStr("GATEWAY_LISTEN_ADDR", ":8080"),
		UpstreamURL:        envStr("GATEWAY_UPSTREAM_URL", ""),
		UpstreamModel:      envStr("GATEWAY_UPSTREAM_MODEL", ""),
		MaxConcurrency:     envInt("GATEWAY_MAX_CONCURRENCY", 2),
		MaxQueueDepth:      envInt("GATEWAY_MAX_QUEUE_DEPTH", 50),
		PriorityQueueDepth: envInt("GATEWAY_PRIORITY_QUEUE_DEPTH", 10),
		RequestTimeout:     envDur("GATEWAY_REQUEST_TIMEOUT", 5*time.Minute),
		AuthToken:          envStr("GATEWAY_AUTH_TOKEN", ""),
		DataFile:           envStr("GATEWAY_DATA_FILE", ""),
		SaveInterval:       envDur("GATEWAY_SAVE_INTERVAL", 60*time.Second),
	}
	if cfg.UpstreamURL == "" {
		return Config{}, fmt.Errorf("GATEWAY_UPSTREAM_URL is required")
	}
	if cfg.MaxConcurrency < 1 {
		return Config{}, fmt.Errorf("GATEWAY_MAX_CONCURRENCY must be >= 1, got %d", cfg.MaxConcurrency)
	}
	if cfg.MaxQueueDepth < 0 {
		return Config{}, fmt.Errorf("GATEWAY_MAX_QUEUE_DEPTH must be >= 0, got %d", cfg.MaxQueueDepth)
	}
	if cfg.RequestTimeout <= 0 {
		return Config{}, fmt.Errorf("GATEWAY_REQUEST_TIMEOUT must be positive")
	}
	return cfg, nil
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func envDur(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
