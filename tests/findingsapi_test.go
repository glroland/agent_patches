package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agent_patches/endpoint-server/findingsapi"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/status"
	"agent_patches/endpoint-server/utils/config"
)

func newFindingsService(t *testing.T) (*findingsapi.Service, *memory.Store) {
	t.Helper()
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	return findingsapi.New(mem), mem
}

func writeTimelineFinding(t *testing.T, mem *memory.Store, entry status.TimelineEntry) {
	t.Helper()
	d := mem.Domain("timeline")
	var entries []status.TimelineEntry
	_ = d.ReadCurrent(&entries)
	entries = append(entries, entry)
	if err := d.Write(entries); err != nil {
		t.Fatalf("write timeline: %v", err)
	}
}

func doResolveFinding(t *testing.T, svc *findingsapi.Service, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/findings/"+id+"/resolve", bytes.NewReader(nil))
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	return rec
}

func TestFindingsAPI_Resolve(t *testing.T) {
	svc, mem := newFindingsService(t)
	writeTimelineFinding(t, mem, status.TimelineEntry{
		ID: "f1", Type: "observation", Title: "New listening port", Severity: "warning",
	})

	rec := doResolveFinding(t, svc, "f1")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body)
	}

	var entries []status.TimelineEntry
	if err := mem.Domain("timeline").ReadCurrent(&entries); err != nil {
		t.Fatalf("ReadCurrent: %v", err)
	}
	if len(entries) != 1 || entries[0].Status == nil || *entries[0].Status != "resolved" {
		t.Errorf("entries = %+v, want single entry with status=resolved", entries)
	}
}

func TestFindingsAPI_ResponseBodyContainsIDAndStatus(t *testing.T) {
	svc, mem := newFindingsService(t)
	writeTimelineFinding(t, mem, status.TimelineEntry{ID: "f2", Type: "observation", Severity: "info"})

	rec := doResolveFinding(t, svc, "f2")

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["id"] != "f2" || resp["status"] != "resolved" {
		t.Errorf("response = %+v, want id=f2 status=resolved", resp)
	}
}

func TestFindingsAPI_NotFound_Returns404(t *testing.T) {
	svc, _ := newFindingsService(t)

	rec := doResolveFinding(t, svc, "nonexistent")

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestFindingsAPI_NoSeverity_Returns400(t *testing.T) {
	svc, mem := newFindingsService(t)
	// An approval entry has no severity — not a resolvable finding.
	writeTimelineFinding(t, mem, status.TimelineEntry{ID: "approval1", Type: "approval"})

	rec := doResolveFinding(t, svc, "approval1")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestFindingsAPI_AlreadyResolved_Idempotent(t *testing.T) {
	svc, mem := newFindingsService(t)
	writeTimelineFinding(t, mem, status.TimelineEntry{ID: "f3", Type: "observation", Severity: "critical"})

	first := doResolveFinding(t, svc, "f3")
	second := doResolveFinding(t, svc, "f3")

	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Errorf("first = %d, second = %d, want both %d", first.Code, second.Code, http.StatusOK)
	}
}

func TestFindingsAPI_NonPost_Returns405(t *testing.T) {
	svc, _ := newFindingsService(t)

	req := httptest.NewRequest(http.MethodGet, "/findings/x/resolve", nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestFindingsAPI_BadPath_Returns404(t *testing.T) {
	svc, _ := newFindingsService(t)

	for _, path := range []string{"/findings//resolve", "/findings/abc/other"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rec := httptest.NewRecorder()
		svc.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("path %q: status = %d, want %d", path, rec.Code, http.StatusNotFound)
		}
	}
}
