package agent

import (
	"testing"

	"agent_patches/endpoint-server/utils/config"
)

// TestNewAgent_ResponsibilityMaxTokens verifies the completion-token budget
// selection: responsibility runs use the tighter responsibility_max_tokens
// cap when configured, while ad-hoc/interactive agents (and responsibility
// runs without a cap) keep the global max_tokens.
func TestNewAgent_ResponsibilityMaxTokens(t *testing.T) {
	cfg := func(maxTok, respMaxTok int) *config.Settings {
		return &config.Settings{Agent: config.AgentSettings{
			MaxTokens:               maxTok,
			ResponsibilityMaxTokens: respMaxTok,
		}}
	}

	cases := []struct {
		name           string
		cfg            *config.Settings
		responsibility string
		want           int
	}{
		{"responsibility capped", cfg(100000, 4096), "disk-space-check", 4096},
		{"responsibility uncapped", cfg(100000, 0), "disk-space-check", 100000},
		{"ad-hoc keeps full budget", cfg(100000, 4096), "", 100000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newAgent(nil, tc.cfg, "sys", tc.responsibility)
			if a.maxTokens != tc.want {
				t.Fatalf("maxTokens = %d, want %d", a.maxTokens, tc.want)
			}
		})
	}
}
