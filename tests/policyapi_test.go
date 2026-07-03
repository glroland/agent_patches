package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/policy"
	"agent_patches/endpoint-server/policyapi"
	"agent_patches/endpoint-server/utils/config"
)

func newPolicyAPI(t *testing.T) (http.Handler, *policy.Store) {
	t.Helper()
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	store := policy.New(mem)
	return policyapi.New(store).Handler(), store
}

func TestPolicyAPI_ListEmpty(t *testing.T) {
	h, _ := newPolicyAPI(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/policies", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /policies status = %d, want 200", rec.Code)
	}
	var body struct {
		Policies []policy.Policy `json:"policies"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body.Policies == nil || len(body.Policies) != 0 {
		t.Errorf("policies = %v, want empty non-nil list", body.Policies)
	}
}

func TestPolicyAPI_CreateListDelete(t *testing.T) {
	h, store := newPolicyAPI(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/policies",
		strings.NewReader(`{"description":"clear rotated logs","pattern":"rm -f /var/log/[a-z.]+\\.gz","risk":"low"}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /policies status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var created policy.Policy
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created policy: %v", err)
	}
	if created.ID == "" || !created.Enabled {
		t.Errorf("created policy = %+v, want non-empty enabled policy", created)
	}

	if p := store.Match("rm -f /var/log/syslog.gz"); p == nil {
		t.Error("store.Match after create: want match")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/policies/"+created.ID, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /policies/%s status = %d, want 204", created.ID, rec.Code)
	}
	if p := store.Match("rm -f /var/log/syslog.gz"); p != nil {
		t.Error("store.Match after delete: want no match")
	}
}

func TestPolicyAPI_CreateInvalidPattern_Returns400(t *testing.T) {
	h, _ := newPolicyAPI(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/policies",
		strings.NewReader(`{"description":"bad","pattern":"rm [","risk":"low"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("POST invalid pattern status = %d, want 400", rec.Code)
	}
}

func TestPolicyAPI_DeleteUnknown_Returns404(t *testing.T) {
	h, _ := newPolicyAPI(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/policies/policy-does-not-exist", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("DELETE unknown status = %d, want 404", rec.Code)
	}
}

func TestPolicyAPI_MethodNotAllowed(t *testing.T) {
	h, _ := newPolicyAPI(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/policies", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT /policies status = %d, want 405", rec.Code)
	}
}
