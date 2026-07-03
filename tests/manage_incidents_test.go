package tests

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/incidents"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skills/manage_incidents"
	"agent_patches/endpoint-server/utils/config"
)

func newManageIncidentsTool(t *testing.T) (tool.Tool, *incidents.Store) {
	t.Helper()
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	store := incidents.New(mem)
	tl, err := manage_incidents.NewManageIncidentsTool(store)
	if err != nil {
		t.Fatalf("NewManageIncidentsTool: %v", err)
	}
	return tl, store
}

func execIncidents(t *testing.T, tl tool.Tool, in map[string]any) (string, error) {
	t.Helper()
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return tl.Execute(context.Background(), b)
}

func TestManageIncidentsTool_FullLifecycle(t *testing.T) {
	tl, store := newManageIncidentsTool(t)

	out, err := execIncidents(t, tl, map[string]any{
		"action": "report", "fingerprint": "disk-full-var",
		"title": "/var above 90%", "detail": "growing steadily", "severity": "warning",
	})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if !strings.Contains(out, "opened new incident") {
		t.Errorf("report output = %q, want opened-new confirmation", out)
	}

	out, err = execIncidents(t, tl, map[string]any{
		"action": "report", "fingerprint": "disk-full-var", "title": "/var above 90%",
	})
	if err != nil {
		t.Fatalf("report recurrence: %v", err)
	}
	if !strings.Contains(out, "seen 2 times") {
		t.Errorf("recurrence output = %q, want recurrence note", out)
	}

	if _, err := execIncidents(t, tl, map[string]any{
		"action": "log_action", "fingerprint": "disk-full-var", "note": "proposed cleanup",
	}); err != nil {
		t.Fatalf("log_action: %v", err)
	}

	out, err = execIncidents(t, tl, map[string]any{"action": "list"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "disk-full-var") || !strings.Contains(out, "proposed cleanup") {
		t.Errorf("list output = %q, want incident with logged action", out)
	}

	if _, err := execIncidents(t, tl, map[string]any{
		"action": "resolve", "fingerprint": "disk-full-var", "note": "cleared logs",
	}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	open, _ := store.Open()
	if len(open) != 0 {
		t.Errorf("open incidents after resolve = %d, want 0", len(open))
	}
}

func TestManageIncidentsTool_RejectsInvalidInput(t *testing.T) {
	tl, _ := newManageIncidentsTool(t)

	if _, err := execIncidents(t, tl, map[string]any{"action": "report", "title": "no fingerprint"}); err == nil {
		t.Error("report without fingerprint: want error")
	}
	if _, err := execIncidents(t, tl, map[string]any{"action": "report", "fingerprint": "x"}); err == nil {
		t.Error("report without title: want error")
	}
	if _, err := execIncidents(t, tl, map[string]any{"action": "explode"}); err == nil {
		t.Error("unknown action: want error")
	}
}
