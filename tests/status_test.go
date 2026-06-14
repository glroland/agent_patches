package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skills/capture_system_info"
	"agent_patches/endpoint-server/status"
	"agent_patches/endpoint-server/utils/config"
)

// fakeCurrentTasker is a test double for the loop's CurrentTask accessor.
type fakeCurrentTasker struct {
	task string
}

func (f fakeCurrentTasker) CurrentTask() string { return f.task }

func newStatusService(t *testing.T, task string) (*status.Service, *memory.Store) {
	t.Helper()
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	info := capture_system_info.Info{
		Hostname:     "host01",
		OS:           "linux",
		Distribution: "Ubuntu",
		Version:      "22.04",
	}
	return status.New(info, mem, fakeCurrentTasker{task: task}), mem
}

func doStatusRequest(t *testing.T, svc *status.Service) status.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	svc.Handler()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp status.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp
}

func TestStatusHandler_Idle(t *testing.T) {
	svc, _ := newStatusService(t, "")

	resp := doStatusRequest(t, svc)

	if resp.Agent.Hostname != "host01" {
		t.Errorf("Agent.Hostname = %q, want %q", resp.Agent.Hostname, "host01")
	}
	if resp.Agent.Platform != "linux" {
		t.Errorf("Agent.Platform = %q, want %q", resp.Agent.Platform, "linux")
	}
	if resp.Agent.OS != "Ubuntu 22.04" {
		t.Errorf("Agent.OS = %q, want %q", resp.Agent.OS, "Ubuntu 22.04")
	}
	if resp.Status.State != "idle" {
		t.Errorf("Status.State = %q, want %q", resp.Status.State, "idle")
	}
	if resp.Status.CurrentTask != nil {
		t.Errorf("Status.CurrentTask = %v, want nil", resp.Status.CurrentTask)
	}
	if resp.Timeline == nil || len(resp.Timeline) != 0 {
		t.Errorf("Timeline = %v, want empty slice", resp.Timeline)
	}
}

func TestStatusHandler_Active(t *testing.T) {
	svc, _ := newStatusService(t, "disk-space-check")

	resp := doStatusRequest(t, svc)

	if resp.Status.State != "active" {
		t.Errorf("Status.State = %q, want %q", resp.Status.State, "active")
	}
	if resp.Status.CurrentTask == nil || *resp.Status.CurrentTask != "disk-space-check" {
		t.Errorf("Status.CurrentTask = %v, want %q", resp.Status.CurrentTask, "disk-space-check")
	}
}

func TestStatusHandler_AttentionFromCriticalSeverity(t *testing.T) {
	svc, mem := newStatusService(t, "")

	entries := []status.TimelineEntry{
		{ID: "1", Type: "observation", Title: "disk full", Severity: "critical"},
	}
	if err := mem.Domain("timeline").Write(entries); err != nil {
		t.Fatalf("write timeline: %v", err)
	}

	resp := doStatusRequest(t, svc)

	if resp.Status.State != "attention" {
		t.Errorf("Status.State = %q, want %q", resp.Status.State, "attention")
	}
	if len(resp.Timeline) != 1 {
		t.Fatalf("Timeline len = %d, want 1", len(resp.Timeline))
	}
}

func TestStatusHandler_AttentionFromPendingApproval(t *testing.T) {
	svc, mem := newStatusService(t, "")

	pending := "pending"
	entries := []status.TimelineEntry{
		{ID: "1", Type: "approval", Title: "apply patches", Status: &pending},
	}
	if err := mem.Domain("timeline").Write(entries); err != nil {
		t.Fatalf("write timeline: %v", err)
	}

	resp := doStatusRequest(t, svc)

	if resp.Status.State != "attention" {
		t.Errorf("Status.State = %q, want %q", resp.Status.State, "attention")
	}
}

func TestStatusHandler_ActiveTakesPrecedenceOverAttention(t *testing.T) {
	svc, mem := newStatusService(t, "keep-system-up-to-date")

	entries := []status.TimelineEntry{
		{ID: "1", Type: "observation", Title: "disk full", Severity: "critical"},
	}
	if err := mem.Domain("timeline").Write(entries); err != nil {
		t.Fatalf("write timeline: %v", err)
	}

	resp := doStatusRequest(t, svc)

	if resp.Status.State != "active" {
		t.Errorf("Status.State = %q, want %q", resp.Status.State, "active")
	}
}
