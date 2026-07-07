package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agent_patches/endpoint-server/loginapi"
	"agent_patches/endpoint-server/loginmonitor"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/utils/config"
)

func TestLoginAPI_Handler_ReturnsActiveAndHistory(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})

	history := []loginmonitor.LoginEvent{
		{
			EventType: loginmonitor.EventExisting,
			SessionID: "1",
			Username:  "alice",
			Class:     "user",
			TTY:       "tty1",
			Timestamp: time.Now().UTC(),
		},
	}
	if err := mem.Attrs().Set("login_history", history); err != nil {
		t.Fatalf("seed login_history: %v", err)
	}
	failed := []loginmonitor.FailedLoginEvent{
		{Username: "root", SourceIP: "10.0.0.5", Reason: "invalid password", Timestamp: time.Now().UTC()},
	}
	if err := mem.Attrs().Set("failed_login_history", failed); err != nil {
		t.Fatalf("seed failed_login_history: %v", err)
	}

	svc := loginapi.New(mem)
	req := httptest.NewRequest(http.MethodGet, "/interactive-logins", nil)
	rec := httptest.NewRecorder()
	svc.Handler()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body)
	}

	var resp loginapi.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Live {
		t.Error("Live = true, want false (history was seeded)")
	}
	if resp.HistoryCount != 1 {
		t.Errorf("HistoryCount = %d, want 1", resp.HistoryCount)
	}
	if resp.FailedCount != 1 {
		t.Errorf("FailedCount = %d, want 1", resp.FailedCount)
	}
	if len(resp.Active) != 1 || resp.Active[0].Username != "alice" {
		t.Fatalf("Active = %+v, want alice's session", resp.Active)
	}
	if len(resp.RecentActivity) != 1 || resp.RecentActivity[0].EventType != "existing" {
		t.Errorf("RecentActivity = %+v, want one 'existing' event", resp.RecentActivity)
	}
	if len(resp.RecentFailedAttempts) != 1 || resp.RecentFailedAttempts[0].Username != "root" {
		t.Errorf("RecentFailedAttempts = %+v, want root's failed attempt", resp.RecentFailedAttempts)
	}
}

func TestLoginAPI_Handler_NoHistory_FallsBackLive(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	svc := loginapi.New(mem)

	req := httptest.NewRequest(http.MethodGet, "/interactive-logins", nil)
	rec := httptest.NewRecorder()
	svc.Handler()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body)
	}

	var resp loginapi.Response
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
	if resp.RecentFailedAttempts == nil || len(resp.RecentFailedAttempts) != 0 {
		t.Errorf("RecentFailedAttempts = %+v, want empty (non-nil) slice", resp.RecentFailedAttempts)
	}
}

func TestLoginAPI_UnsupportedMethod_Returns405(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	svc := loginapi.New(mem)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/interactive-logins", nil)
		rec := httptest.NewRecorder()
		svc.Handler()(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /interactive-logins: status = %d, want %d", method, rec.Code, http.StatusMethodNotAllowed)
		}
	}
}
