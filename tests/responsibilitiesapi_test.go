package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	tasks "agent_patches/endpoint-server/a2a/registry"
	"agent_patches/endpoint-server/incidents"
	"agent_patches/endpoint-server/loop"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/responsibilitiesapi"
	"agent_patches/endpoint-server/utils/config"
)

func newResponsibilitiesService(t *testing.T) (*responsibilitiesapi.Service, *memory.Store) {
	t.Helper()
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	cfg := &config.Settings{
		Loop: config.LoopSettings{Heartbeat: "1s"},
		Responsibilities: []config.ResponsibilitySettings{
			{
				Name:        "disk-space-check",
				Frequency:   "1h",
				Instruction: "Check disk space",
				Tools:       []string{"check_drives", "report_findings"},
			},
			{
				Name:        "daily-patch-check",
				Time:        "03:00",
				Instruction: "Check for patches",
			},
		},
	}
	lp := loop.New(cfg, tasksRegistry(), nil, mem, incidents.New(mem))
	return responsibilitiesapi.New(lp, mem), mem
}

func tasksRegistry() *tasks.Registry {
	return tasks.NewRegistry()
}

func TestResponsibilitiesAPI_ListsConfiguredResponsibilities(t *testing.T) {
	svc, _ := newResponsibilitiesService(t)

	req := httptest.NewRequest(http.MethodGet, "/responsibilities", nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}

	var items []responsibilitiesapi.ResponsibilityItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}

	byName := map[string]responsibilitiesapi.ResponsibilityItem{}
	for _, it := range items {
		byName[it.Name] = it
	}

	disk, ok := byName["disk-space-check"]
	if !ok {
		t.Fatal("disk-space-check missing")
	}
	if disk.Status != "never" {
		t.Errorf("status = %q, want never (no runs yet)", disk.Status)
	}
	if disk.Instruction != "Check disk space" {
		t.Errorf("instruction = %q", disk.Instruction)
	}
	if len(disk.Tools) != 2 {
		t.Errorf("tools = %v", disk.Tools)
	}
	if disk.NextRunAt == nil {
		t.Error("frequency responsibility has no NextRunAt")
	}

	if _, ok := byName["daily-patch-check"]; !ok {
		t.Fatal("daily-patch-check missing")
	}
}

func TestResponsibilitiesAPI_OverlaysPersistedRunState(t *testing.T) {
	svc, mem := newResponsibilitiesService(t)

	state := loop.RunState{
		LastRunAt: "2026-07-11T03:00:00Z",
		Status:    "ok",
		Summary:   "all disks healthy",
	}
	if err := mem.Attrs().Set(loop.AttrRunPrefix+"disk-space-check", state); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/responsibilities", nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)

	var items []responsibilitiesapi.ResponsibilityItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, it := range items {
		if it.Name != "disk-space-check" {
			continue
		}
		if it.Status != "ok" || it.Summary != "all disks healthy" {
			t.Errorf("item = %+v, want persisted run state overlaid", it)
		}
		if it.LastRunAt == nil || *it.LastRunAt != "2026-07-11T03:00:00Z" {
			t.Errorf("lastRunAt = %v", it.LastRunAt)
		}
		return
	}
	t.Fatal("disk-space-check missing")
}

func TestResponsibilitiesAPI_MethodNotAllowed(t *testing.T) {
	svc, _ := newResponsibilitiesService(t)

	req := httptest.NewRequest(http.MethodPost, "/responsibilities", nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}
