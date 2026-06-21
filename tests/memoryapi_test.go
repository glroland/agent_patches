package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/memoryapi"
	"agent_patches/endpoint-server/utils/config"
)

func newMemoryAPIService(t *testing.T) (*memoryapi.Service, *memory.Store) {
	t.Helper()
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	return memoryapi.New(mem), mem
}

func TestMemoryAPI_Handler_ReturnsDomainsAndAttrs(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})

	if err := mem.Domain("disk").Write(map[string]any{"usedPct": 42}); err != nil {
		t.Fatalf("Write domain: %v", err)
	}
	if err := mem.Attrs().Set("skill_state:check_drives", map[string]string{"health": "ok"}); err != nil {
		t.Fatalf("Set attr: %v", err)
	}

	svc := memoryapi.New(mem)

	req := httptest.NewRequest(http.MethodGet, "/memory", nil)
	rec := httptest.NewRecorder()
	svc.Handler()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusOK)
	}

	var dump memory.Dump
	if err := json.Unmarshal(rec.Body.Bytes(), &dump); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if _, ok := dump.Domains["disk"]; !ok {
		t.Errorf("response missing domain %q: %+v", "disk", dump.Domains)
	}
	if _, ok := dump.Attrs["skill_state:check_drives"]; !ok {
		t.Errorf("response missing attr %q: %+v", "skill_state:check_drives", dump.Attrs)
	}
}

func TestMemoryAPI_Delete_ClearsMemory(t *testing.T) {
	svc, mem := newMemoryAPIService(t)

	if err := mem.Domain("disk").Write(map[string]any{"usedPct": 99}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := mem.Attrs().Set("k", "v"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/memory", nil)
	rec := httptest.NewRecorder()
	svc.Handler()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body)
	}

	var resp map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp["cleared"] {
		t.Errorf("response[cleared] = %v, want true", resp["cleared"])
	}

	// Memory should now be empty.
	dump, err := mem.Dump()
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if len(dump.Domains) != 0 || len(dump.Attrs) != 0 {
		t.Errorf("memory not cleared: domains=%v attrs=%v", dump.Domains, dump.Attrs)
	}
}

func TestMemoryAPI_UnsupportedMethod_Returns405(t *testing.T) {
	svc, _ := newMemoryAPIService(t)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch} {
		req := httptest.NewRequest(method, "/memory", nil)
		rec := httptest.NewRecorder()
		svc.Handler()(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /memory: status = %d, want %d", method, rec.Code, http.StatusMethodNotAllowed)
		}
	}
}

func TestMemoryAPI_EmptyStore_ReturnsEmptyDump(t *testing.T) {
	svc, _ := newMemoryAPIService(t)

	req := httptest.NewRequest(http.MethodGet, "/memory", nil)
	rec := httptest.NewRecorder()
	svc.Handler()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var dump memory.Dump
	if err := json.Unmarshal(rec.Body.Bytes(), &dump); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(dump.Domains) != 0 || len(dump.Attrs) != 0 {
		t.Errorf("empty store dump non-empty: %+v", dump)
	}
}
