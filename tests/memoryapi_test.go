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
