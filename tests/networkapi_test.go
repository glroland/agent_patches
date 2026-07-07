package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agent_patches/endpoint-server/connmonitor"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/networkapi"
	"agent_patches/endpoint-server/utils/config"
)

func TestNetworkAPI_Handler_ReturnsActiveAndHistory(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})

	gather := func(context.Context) ([]connmonitor.Conn, error) {
		return []connmonitor.Conn{
			{Proto: "tcp", LocalAddr: "192.168.1.5", LocalPort: 22, RemoteAddr: "192.168.1.10", RemotePort: 54321, State: "ESTABLISHED", Process: "sshd"},
		}, nil
	}
	mon := connmonitor.NewWithGatherer(mem, config.NetworkMonitorSettings{}, gather)
	if _, err := mon.PollOnce(context.Background()); err != nil {
		t.Fatalf("seed PollOnce: %v", err)
	}

	svc := networkapi.New(mem)
	req := httptest.NewRequest(http.MethodGet, "/network-connections", nil)
	rec := httptest.NewRecorder()
	svc.Handler()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body)
	}

	var resp networkapi.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Live {
		t.Error("Live = true, want false (history was seeded)")
	}
	if resp.HistoryCount != 1 {
		t.Errorf("HistoryCount = %d, want 1", resp.HistoryCount)
	}
	if len(resp.Active) != 1 {
		t.Fatalf("Active = %+v, want 1 entry", resp.Active)
	}
	if resp.Active[0].Process != "sshd" || resp.Active[0].Direction != "inbound" {
		t.Errorf("Active[0] = %+v, want sshd/inbound", resp.Active[0])
	}
	if len(resp.RecentActivity) != 1 || resp.RecentActivity[0].EventType != "existing" {
		t.Errorf("RecentActivity = %+v, want one 'existing' event", resp.RecentActivity)
	}
}

func TestNetworkAPI_Handler_NoHistory_FallsBackLive(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	svc := networkapi.New(mem)

	req := httptest.NewRequest(http.MethodGet, "/network-connections", nil)
	rec := httptest.NewRecorder()
	svc.Handler()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body)
	}

	var resp networkapi.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Live {
		t.Error("Live = false, want true (no history recorded yet)")
	}
	if resp.Note == "" {
		t.Error("Note is empty, want an explanation of the live fallback")
	}
	if resp.RecentActivity == nil || len(resp.RecentActivity) != 0 {
		t.Errorf("RecentActivity = %+v, want empty (non-nil) slice", resp.RecentActivity)
	}
}

func TestNetworkAPI_UnsupportedMethod_Returns405(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	svc := networkapi.New(mem)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/network-connections", nil)
		rec := httptest.NewRecorder()
		svc.Handler()(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /network-connections: status = %d, want %d", method, rec.Code, http.StatusMethodNotAllowed)
		}
	}
}
