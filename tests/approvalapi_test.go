package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agent_patches/endpoint-server/approvalapi"
	"agent_patches/endpoint-server/memory"
	reqapproval "agent_patches/endpoint-server/skills/request_approval"
	"agent_patches/endpoint-server/utils/config"
)

func newApprovalService(t *testing.T) (*approvalapi.Service, *memory.Store) {
	t.Helper()
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	return approvalapi.New(mem, nil, nil), mem
}

func writePendingApproval(t *testing.T, mem *memory.Store, id string) {
	t.Helper()
	entry := reqapproval.ApprovalEntry{
		ID:             id,
		Title:          "Apply patches",
		Detail:         "3 updates pending",
		ProposedAction: "apt-get upgrade -y",
		Risk:           "medium",
		Status:         "pending",
		RequestedAt:    time.Now(),
	}
	if err := mem.Attrs().Set(reqapproval.AttrsKey(id), entry); err != nil {
		t.Fatalf("write approval: %v", err)
	}
}

func doApprovalDecision(t *testing.T, svc *approvalapi.Service, id, decision string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"decision": decision})
	req := httptest.NewRequest(http.MethodPost, "/approvals/"+id+"/decision", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	return rec
}

func TestApprovalAPI_Approve(t *testing.T) {
	svc, mem := newApprovalService(t)
	writePendingApproval(t, mem, "abc123")

	rec := doApprovalDecision(t, svc, "abc123", "approved")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body)
	}

	var entry reqapproval.ApprovalEntry
	if err := mem.Attrs().Get(reqapproval.AttrsKey("abc123"), &entry); err != nil {
		t.Fatalf("read entry: %v", err)
	}
	if entry.Status != "approved" {
		t.Errorf("entry.Status = %q, want approved", entry.Status)
	}
	if entry.DecidedAt == nil {
		t.Error("entry.DecidedAt should be set after decision")
	}
}

func TestApprovalAPI_Reject(t *testing.T) {
	svc, mem := newApprovalService(t)
	writePendingApproval(t, mem, "def456")

	rec := doApprovalDecision(t, svc, "def456", "rejected")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body)
	}

	var entry reqapproval.ApprovalEntry
	if err := mem.Attrs().Get(reqapproval.AttrsKey("def456"), &entry); err != nil {
		t.Fatalf("read entry: %v", err)
	}
	if entry.Status != "rejected" {
		t.Errorf("entry.Status = %q, want rejected", entry.Status)
	}
}

func TestApprovalAPI_RejectWithReason(t *testing.T) {
	svc, mem := newApprovalService(t)
	writePendingApproval(t, mem, "ghi789")

	body, _ := json.Marshal(map[string]string{"decision": "rejected", "reason": "not during business hours"})
	req := httptest.NewRequest(http.MethodPost, "/approvals/ghi789/decision", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body)
	}

	var entry reqapproval.ApprovalEntry
	_ = mem.Attrs().Get(reqapproval.AttrsKey("ghi789"), &entry)
	if entry.Reason != "not during business hours" {
		t.Errorf("entry.Reason = %q, want %q", entry.Reason, "not during business hours")
	}
}

func TestApprovalAPI_NotFound_Returns404(t *testing.T) {
	svc, _ := newApprovalService(t)

	rec := doApprovalDecision(t, svc, "nonexistent", "approved")

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestApprovalAPI_AlreadyDecided_Returns409(t *testing.T) {
	svc, mem := newApprovalService(t)
	writePendingApproval(t, mem, "dup001")
	doApprovalDecision(t, svc, "dup001", "approved")

	// Second decision on already-approved entry.
	rec := doApprovalDecision(t, svc, "dup001", "rejected")

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d (already decided)", rec.Code, http.StatusConflict)
	}
}

func TestApprovalAPI_InvalidDecision_Returns400(t *testing.T) {
	svc, mem := newApprovalService(t)
	writePendingApproval(t, mem, "bad001")

	rec := doApprovalDecision(t, svc, "bad001", "maybe")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d (invalid decision)", rec.Code, http.StatusBadRequest)
	}
}

func TestApprovalAPI_InvalidJSON_Returns400(t *testing.T) {
	svc, _ := newApprovalService(t)

	req := httptest.NewRequest(http.MethodPost, "/approvals/x/decision", bytes.NewReader([]byte("not json")))
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d (invalid JSON)", rec.Code, http.StatusBadRequest)
	}
}

func TestApprovalAPI_NonPost_Returns405(t *testing.T) {
	svc, _ := newApprovalService(t)

	req := httptest.NewRequest(http.MethodGet, "/approvals/x/decision", nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestApprovalAPI_BadPath_Returns404(t *testing.T) {
	svc, _ := newApprovalService(t)

	for _, path := range []string{"/approvals//decision", "/approvals/abc/other"} {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(`{"decision":"approved"}`)))
		rec := httptest.NewRecorder()
		svc.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("path %q: status = %d, want %d", path, rec.Code, http.StatusNotFound)
		}
	}
}

func TestApprovalAPI_ResponseBodyContainsIDAndStatus(t *testing.T) {
	svc, mem := newApprovalService(t)
	writePendingApproval(t, mem, "resp001")

	rec := doApprovalDecision(t, svc, "resp001", "approved")

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["id"] != "resp001" {
		t.Errorf("response id = %q, want %q", resp["id"], "resp001")
	}
	if resp["status"] != "approved" {
		t.Errorf("response status = %q, want %q", resp["status"], "approved")
	}
}

// ---- AttrsKey ---------------------------------------------------------------

func TestAttrsKey_Format(t *testing.T) {
	got := reqapproval.AttrsKey("abc123")
	if got != "approval:abc123" {
		t.Errorf("AttrsKey = %q, want %q", got, "approval:abc123")
	}
}

// ---- PatchTimeline ----------------------------------------------------------

func TestPatchTimeline_UpdatesStatus(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})

	// Write a timeline with a pending approval entry.
	pending := "pending"
	entries := []interface{}{
		map[string]interface{}{
			"id": "tid001", "type": "approval", "title": "patch",
			"status": &pending,
		},
	}
	if err := mem.Domain("timeline").Write(entries); err != nil {
		t.Fatalf("write timeline: %v", err)
	}

	if err := reqapproval.PatchTimeline(mem, "tid001", "approved"); err != nil {
		t.Fatalf("PatchTimeline: %v", err)
	}

	// Re-read the timeline and verify the status was updated.
	var raw []map[string]interface{}
	if err := mem.Domain("timeline").ReadCurrent(&raw); err != nil {
		t.Fatalf("ReadCurrent: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("timeline len = %d, want 1", len(raw))
	}
	if raw[0]["status"] != "approved" {
		t.Errorf("timeline[0].status = %v, want approved", raw[0]["status"])
	}
}

func TestPatchTimeline_NonMatchingID_LeavesTimelineUnchanged(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})

	// Write a timeline that does NOT contain the ID we will try to patch.
	pending := "pending"
	entries := []interface{}{
		map[string]interface{}{
			"id": "real-entry", "type": "approval", "title": "patch", "status": &pending,
		},
	}
	if err := mem.Domain("timeline").Write(entries); err != nil {
		t.Fatalf("write timeline: %v", err)
	}

	// Patching a non-existent ID should return no error and leave the timeline intact.
	if err := reqapproval.PatchTimeline(mem, "nobody", "approved"); err != nil {
		t.Errorf("PatchTimeline with non-matching ID returned error: %v", err)
	}

	var raw []map[string]interface{}
	if err := mem.Domain("timeline").ReadCurrent(&raw); err != nil {
		t.Fatalf("ReadCurrent: %v", err)
	}
	if len(raw) != 1 {
		t.Errorf("timeline len = %d, want 1 (entry should be untouched)", len(raw))
	}
}
