package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skills/capture_system_info"
	"agent_patches/endpoint-server/skillstate"
	"agent_patches/endpoint-server/status"
	"agent_patches/endpoint-server/utils/config"
	"agent_patches/endpoint-server/utils/storage"
)

// fakeCurrentTasker is a test double for the loop's RunningTasks accessor.
type fakeCurrentTasker struct {
	task string
}

func (f fakeCurrentTasker) RunningTasks() []string {
	if f.task == "" {
		return nil
	}
	return []string{f.task}
}

func newStatusService(t *testing.T, task string) (*status.Service, *memory.Store) {
	t.Helper()
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	info := capture_system_info.Info{
		Hostname:     "host01",
		OS:           "linux",
		Distribution: "Ubuntu",
		Version:      "22.04",
	}
	tasksStore := storage.NewStore(filepath.Join(t.TempDir(), "tasks.jsonl"))
	return status.New(info, mem, fakeCurrentTasker{task: task}, &config.Settings{}, tasksStore), mem
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

func TestStatusHandler_AttentionFromPendingApprovalHighRisk(t *testing.T) {
	svc, mem := newStatusService(t, "")

	pending := "pending"
	entries := []status.TimelineEntry{
		{ID: "1", Type: "approval", Title: "apply patches", Status: &pending, Risk: "high"},
	}
	if err := mem.Domain("timeline").Write(entries); err != nil {
		t.Fatalf("write timeline: %v", err)
	}

	resp := doStatusRequest(t, svc)

	if resp.Status.State != "attention" {
		t.Errorf("Status.State = %q, want %q", resp.Status.State, "attention")
	}
}

func TestStatusHandler_AttentionFromPendingApprovalMediumRisk(t *testing.T) {
	svc, mem := newStatusService(t, "")

	pending := "pending"
	entries := []status.TimelineEntry{
		{ID: "1", Type: "approval", Title: "apply patches", Status: &pending, Risk: "medium"},
	}
	if err := mem.Domain("timeline").Write(entries); err != nil {
		t.Fatalf("write timeline: %v", err)
	}

	resp := doStatusRequest(t, svc)

	if resp.Status.State != "attention" {
		t.Errorf("Status.State = %q, want %q", resp.Status.State, "attention")
	}
}

// Low-risk pending approvals (routine patches with no CVEs) must NOT flip the
// agent to "attention" — available updates alone do not make a server unhealthy.
func TestStatusHandler_LowRiskApprovalDoesNotTriggerAttention(t *testing.T) {
	svc, mem := newStatusService(t, "")

	pending := "pending"
	entries := []status.TimelineEntry{
		{ID: "1", Type: "approval", Title: "apply patches", Status: &pending, Risk: "low"},
	}
	if err := mem.Domain("timeline").Write(entries); err != nil {
		t.Fatalf("write timeline: %v", err)
	}

	resp := doStatusRequest(t, svc)

	if resp.Status.State != "idle" {
		t.Errorf("Status.State = %q, want %q (low-risk approval should not trigger attention)", resp.Status.State, "idle")
	}
}

// An already-decided (approved/rejected) approval must not re-trigger attention.
func TestStatusHandler_ResolvedApprovalDoesNotTriggerAttention(t *testing.T) {
	svc, mem := newStatusService(t, "")

	approved := "approved"
	entries := []status.TimelineEntry{
		{ID: "1", Type: "approval", Title: "apply patches", Status: &approved, Risk: "high"},
	}
	if err := mem.Domain("timeline").Write(entries); err != nil {
		t.Fatalf("write timeline: %v", err)
	}

	resp := doStatusRequest(t, svc)

	if resp.Status.State != "idle" {
		t.Errorf("Status.State = %q, want idle (resolved approval should not trigger attention)", resp.Status.State)
	}
}

func TestStatusHandler_LastPatchedAt_FromAttrs(t *testing.T) {
	svc, mem := newStatusService(t, "")

	const ts = "2025-03-15T10:00:00Z"
	if err := mem.Attrs().Set("last_patched_at", ts); err != nil {
		t.Fatalf("Set last_patched_at: %v", err)
	}

	resp := doStatusRequest(t, svc)

	if resp.LastPatchedAt == nil {
		t.Fatal("LastPatchedAt is nil, want non-nil")
	}
	if *resp.LastPatchedAt != ts {
		t.Errorf("LastPatchedAt = %q, want %q", *resp.LastPatchedAt, ts)
	}
}

func TestStatusHandler_OsLabel_WithDistribAndVersion(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	info := capture_system_info.Info{Hostname: "h", OS: "linux", Distribution: "Ubuntu", Version: "24.04"}
	svc := status.New(info, mem, fakeCurrentTasker{}, &config.Settings{}, nil)

	resp := doStatusRequest(t, svc)
	if resp.Agent.OS != "Ubuntu 24.04" {
		t.Errorf("Agent.OS = %q, want %q", resp.Agent.OS, "Ubuntu 24.04")
	}
}

func TestStatusHandler_OsLabel_DistribOnly(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	info := capture_system_info.Info{Hostname: "h", OS: "linux", Distribution: "Debian"}
	svc := status.New(info, mem, fakeCurrentTasker{}, &config.Settings{}, nil)

	resp := doStatusRequest(t, svc)
	if resp.Agent.OS != "Debian" {
		t.Errorf("Agent.OS = %q, want %q", resp.Agent.OS, "Debian")
	}
}

func TestStatusHandler_OsLabel_FallsBackToGOOS(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	info := capture_system_info.Info{Hostname: "h", OS: "windows"}
	svc := status.New(info, mem, fakeCurrentTasker{}, &config.Settings{}, nil)

	resp := doStatusRequest(t, svc)
	if resp.Agent.OS != "windows" {
		t.Errorf("Agent.OS = %q, want %q", resp.Agent.OS, "windows")
	}
}

func TestStatusHandler_SkillStateWarningDoesNotTriggerAttention(t *testing.T) {
	svc, mem := newStatusService(t, "")

	if err := skillstate.Save(mem, "analyze_memory_utilization", skillstate.HealthWarning, "RAM used: 82%"); err != nil {
		t.Fatalf("skillstate.Save: %v", err)
	}

	resp := doStatusRequest(t, svc)

	// Warning-level skill state must appear in the timeline but not flip to attention.
	if resp.Status.State != "idle" {
		t.Errorf("Status.State = %q, want idle (warning skill state should not trigger attention)", resp.Status.State)
	}
	found := false
	for _, e := range resp.Timeline {
		if strings.Contains(e.ID, "analyze_memory_utilization") {
			found = true
			if e.Severity != "warning" {
				t.Errorf("timeline entry severity = %q, want warning", e.Severity)
			}
		}
	}
	if !found {
		t.Error("warning skill state should appear in timeline even though it does not trigger attention")
	}
}

func TestStatusHandler_AttentionFromSkillState(t *testing.T) {
	svc, mem := newStatusService(t, "")

	if err := skillstate.Save(mem, "check_drives", skillstate.HealthCritical, "/ is 94.7% full; SMART status FAILED for /dev/sda"); err != nil {
		t.Fatalf("skillstate.Save: %v", err)
	}

	resp := doStatusRequest(t, svc)

	if resp.Status.State != "attention" {
		t.Errorf("Status.State = %q, want %q", resp.Status.State, "attention")
	}
	if len(resp.Timeline) != 1 {
		t.Fatalf("Timeline len = %d, want 1", len(resp.Timeline))
	}
	if resp.Timeline[0].Severity != "critical" {
		t.Errorf("Timeline[0].Severity = %q, want %q", resp.Timeline[0].Severity, "critical")
	}
	if resp.Timeline[0].ID != "skillstate:check_drives" {
		t.Errorf("Timeline[0].ID = %q, want %q", resp.Timeline[0].ID, "skillstate:check_drives")
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
