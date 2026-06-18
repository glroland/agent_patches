package tests

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agent_patches/endpoint-server/logind"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skills/check_interactive_logins"
	"agent_patches/endpoint-server/utils/config"
)

func TestLoginSessions_BuildReport_LocalSession(t *testing.T) {
	sessions := []logind.SessionInfo{
		{
			ID:          "1",
			Username:    "alice",
			Class:       "user",
			SessionType: "tty",
			TTY:         "tty1",
			Leader:      1234,
			Timestamp:   time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
		},
	}
	report := logind.BuildReport(sessions)

	for _, want := range []string{"alice", "tty1", "local console", "1234"} {
		if !strings.Contains(report, want) {
			t.Errorf("BuildReport missing %q\nfull:\n%s", want, report)
		}
	}
}

func TestLoginSessions_BuildReport_RemoteSession(t *testing.T) {
	sessions := []logind.SessionInfo{
		{
			ID:          "2",
			Username:    "bob",
			SessionType: "tty",
			Remote:      true,
			RemoteHost:  "10.0.0.5",
			RemoteUser:  "bob",
		},
	}
	report := logind.BuildReport(sessions)

	for _, want := range []string{"bob", "remote", "10.0.0.5"} {
		if !strings.Contains(report, want) {
			t.Errorf("BuildReport missing %q\nfull:\n%s", want, report)
		}
	}
}

func TestNewLoginSessionsTool_NameAndDescription(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tl, err := check_interactive_logins.NewLoginSessionsTool(mem)
	if err != nil {
		t.Fatalf("NewLoginSessionsTool() unexpected error: %v", err)
	}
	if got := tl.Name(); got != "check_interactive_logins" {
		t.Errorf("Name() = %q, want %q", got, "check_interactive_logins")
	}
	if tl.Description() == "" {
		t.Error("Description() returned empty string")
	}
}

func TestLoginSessionsTool_Execute_DoesNotError(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tl, err := check_interactive_logins.NewLoginSessionsTool(mem)
	if err != nil {
		t.Fatalf("NewLoginSessionsTool() unexpected error: %v", err)
	}

	input, _ := json.Marshal(struct{}{})
	if _, err := tl.Execute(context.Background(), input); err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
}
