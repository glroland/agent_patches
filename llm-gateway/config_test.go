package main

import (
	"testing"
	"time"
)

// clearGatewayEnv unsets every GATEWAY_* variable the config reads so a
// developer's shell environment cannot leak into test expectations.
func clearGatewayEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"GATEWAY_LISTEN_ADDR", "GATEWAY_UPSTREAM_URL", "GATEWAY_UPSTREAM_MODEL",
		"GATEWAY_MAX_CONCURRENCY", "GATEWAY_MAX_QUEUE_DEPTH", "GATEWAY_PRIORITY_QUEUE_DEPTH",
		"GATEWAY_REQUEST_TIMEOUT", "GATEWAY_AUTH_TOKEN", "GATEWAY_DATA_FILE", "GATEWAY_SAVE_INTERVAL",
	} {
		t.Setenv(k, "")
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	clearGatewayEnv(t)
	t.Setenv("GATEWAY_UPSTREAM_URL", "http://llm:11434")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want :8080", cfg.ListenAddr)
	}
	if cfg.MaxConcurrency != 2 {
		t.Errorf("MaxConcurrency = %d, want 2", cfg.MaxConcurrency)
	}
	if cfg.MaxQueueDepth != 50 {
		t.Errorf("MaxQueueDepth = %d, want 50", cfg.MaxQueueDepth)
	}
	if cfg.PriorityQueueDepth != 10 {
		t.Errorf("PriorityQueueDepth = %d, want 10", cfg.PriorityQueueDepth)
	}
	if cfg.RequestTimeout != 5*time.Minute {
		t.Errorf("RequestTimeout = %v, want 5m", cfg.RequestTimeout)
	}
	if cfg.SaveInterval != 60*time.Second {
		t.Errorf("SaveInterval = %v, want 60s", cfg.SaveInterval)
	}
}

func TestLoadConfig_MissingUpstreamURL(t *testing.T) {
	clearGatewayEnv(t)
	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig succeeded without GATEWAY_UPSTREAM_URL, want error")
	}
}

func TestLoadConfig_InvalidValues(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{"zero concurrency", "GATEWAY_MAX_CONCURRENCY", "0"},
		{"negative queue depth", "GATEWAY_MAX_QUEUE_DEPTH", "-1"},
		{"non-positive timeout", "GATEWAY_REQUEST_TIMEOUT", "-5s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearGatewayEnv(t)
			t.Setenv("GATEWAY_UPSTREAM_URL", "http://llm:11434")
			t.Setenv(tc.key, tc.value)
			if _, err := LoadConfig(); err == nil {
				t.Fatalf("LoadConfig succeeded with %s=%s, want error", tc.key, tc.value)
			}
		})
	}
}

func TestLoadConfig_MalformedNumbersFallBackToDefaults(t *testing.T) {
	clearGatewayEnv(t)
	t.Setenv("GATEWAY_UPSTREAM_URL", "http://llm:11434")
	t.Setenv("GATEWAY_MAX_CONCURRENCY", "not-a-number")
	t.Setenv("GATEWAY_REQUEST_TIMEOUT", "not-a-duration")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.MaxConcurrency != 2 {
		t.Errorf("MaxConcurrency = %d, want default 2", cfg.MaxConcurrency)
	}
	if cfg.RequestTimeout != 5*time.Minute {
		t.Errorf("RequestTimeout = %v, want default 5m", cfg.RequestTimeout)
	}
}
