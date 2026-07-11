package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agent_patches/endpoint-server/manualrunapi"
	"agent_patches/endpoint-server/memory"
	reqmanualrun "agent_patches/endpoint-server/skills/request_manual_run"
	"agent_patches/endpoint-server/status"
	"agent_patches/endpoint-server/utils/config"
)

func newManualRunService(t *testing.T) (*manualrunapi.Service, *memory.Store) {
	t.Helper()
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	return manualrunapi.New(mem), mem
}

func writePendingManualRun(t *testing.T, mem *memory.Store, id string) {
	t.Helper()
	entry := reqmanualrun.ManualRunEntry{
		ID:          id,
		Title:       "Restart nginx",
		Command:     "systemctl restart nginx",
		Host:        "web-1",
		Reason:      "sudoers restriction",
		Status:      "pending",
		RequestedAt: time.Now(),
	}
	if err := mem.Attrs().Set(reqmanualrun.AttrsKey(id), entry); err != nil {
		t.Fatalf("write manual run entry: %v", err)
	}
}

func doManualRunResult(t *testing.T, svc *manualrunapi.Service, path string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	return rec
}

func TestManualRunAPI_Completed(t *testing.T) {
	svc, mem := newManualRunService(t)
	writePendingManualRun(t, mem, "mr-1")

	rec := doManualRunResult(t, svc, "/manual-runs/mr-1/result",
		map[string]string{"status": "completed", "output": "nginx restarted"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}

	var entry reqmanualrun.ManualRunEntry
	if err := mem.Attrs().Get(reqmanualrun.AttrsKey("mr-1"), &entry); err != nil {
		t.Fatalf("read entry back: %v", err)
	}
	if entry.Status != "completed" || entry.Output != "nginx restarted" {
		t.Errorf("entry = %+v, want completed with output", entry)
	}
	if entry.CompletedAt == nil {
		t.Error("CompletedAt not set")
	}
}

func TestManualRunAPI_Skipped(t *testing.T) {
	svc, mem := newManualRunService(t)
	writePendingManualRun(t, mem, "mr-2")

	rec := doManualRunResult(t, svc, "/manual-runs/mr-2/result",
		map[string]string{"status": "skipped"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var entry reqmanualrun.ManualRunEntry
	if err := mem.Attrs().Get(reqmanualrun.AttrsKey("mr-2"), &entry); err != nil {
		t.Fatalf("read entry back: %v", err)
	}
	if entry.Status != "skipped" {
		t.Errorf("status = %q, want skipped", entry.Status)
	}
}

func TestManualRunAPI_InvalidStatus(t *testing.T) {
	svc, mem := newManualRunService(t)
	writePendingManualRun(t, mem, "mr-3")

	rec := doManualRunResult(t, svc, "/manual-runs/mr-3/result",
		map[string]string{"status": "approved"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestManualRunAPI_AlreadyResolved(t *testing.T) {
	svc, mem := newManualRunService(t)
	writePendingManualRun(t, mem, "mr-4")

	if rec := doManualRunResult(t, svc, "/manual-runs/mr-4/result",
		map[string]string{"status": "completed", "output": "done"}); rec.Code != http.StatusOK {
		t.Fatalf("first submission = %d", rec.Code)
	}
	rec := doManualRunResult(t, svc, "/manual-runs/mr-4/result",
		map[string]string{"status": "completed", "output": "again"})
	if rec.Code != http.StatusConflict {
		t.Errorf("second submission = %d, want 409", rec.Code)
	}
}

func TestManualRunAPI_UnknownIDRemovesStaleTimelineEntry(t *testing.T) {
	svc, mem := newManualRunService(t)

	// A timeline card exists for an entry that is gone from attrs (e.g. the
	// agent restarted after the run was processed). The handler should 404
	// and clean up the orphaned card.
	stale := "stale-id"
	pending := "pending"
	if err := mem.Domain("timeline").Write([]status.TimelineEntry{{
		ID: stale, Type: "manual_run", Title: "orphan", Status: &pending,
	}}); err != nil {
		t.Fatal(err)
	}

	rec := doManualRunResult(t, svc, "/manual-runs/"+stale+"/result",
		map[string]string{"status": "completed"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}

	var entries []status.TimelineEntry
	if err := mem.Domain("timeline").ReadCurrent(&entries); err != nil {
		t.Fatalf("read timeline: %v", err)
	}
	for _, e := range entries {
		if e.ID == stale {
			t.Error("stale timeline entry not removed")
		}
	}
}

func TestManualRunAPI_BadPaths(t *testing.T) {
	svc, _ := newManualRunService(t)

	for _, path := range []string{"/manual-runs/", "/manual-runs/id-only", "/manual-runs/id/notresult"} {
		rec := doManualRunResult(t, svc, path, map[string]string{"status": "completed"})
		if rec.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", path, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/manual-runs/x/result", nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d, want 405", rec.Code)
	}
}
