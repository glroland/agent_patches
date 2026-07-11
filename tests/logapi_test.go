package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent_patches/endpoint-server/logapi"
)

func doLogRequest(t *testing.T, svc *logapi.Service, method string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/log", nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	return rec
}

func decodeLogResponse(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v; body: %s", err, rec.Body)
	}
	return body
}

func TestLogAPI_NoLogFileConfigured(t *testing.T) {
	svc := logapi.New("")
	rec := doLogRequest(t, svc, http.MethodGet)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := decodeLogResponse(t, rec)
	if body["content"] != "" || body["truncated"] != false {
		t.Errorf("body = %v, want empty content, truncated=false", body)
	}
	if note, _ := body["note"].(string); !strings.Contains(note, "stderr") {
		t.Errorf("note = %q, want mention of stderr", note)
	}
}

func TestLogAPI_FileDoesNotExistYet(t *testing.T) {
	svc := logapi.New(filepath.Join(t.TempDir(), "missing.log"))
	rec := doLogRequest(t, svc, http.MethodGet)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := decodeLogResponse(t, rec)
	if note, _ := body["note"].(string); !strings.Contains(note, "does not exist") {
		t.Errorf("note = %q, want mention of missing file", note)
	}
}

func TestLogAPI_ReturnsFileContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	content := "line one\nline two\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := logapi.New(path)
	rec := doLogRequest(t, svc, http.MethodGet)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := decodeLogResponse(t, rec)
	if body["content"] != content {
		t.Errorf("content = %q, want %q", body["content"], content)
	}
	if body["truncated"] != false {
		t.Error("small file reported as truncated")
	}
}

func TestLogAPI_TruncatesLargeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	// 1 MB limit; write 1 MB + 100 bytes so only the tail is returned.
	big := strings.Repeat("a", 100) + strings.Repeat("b", 1<<20)
	if err := os.WriteFile(path, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := logapi.New(path)
	rec := doLogRequest(t, svc, http.MethodGet)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := decodeLogResponse(t, rec)
	got, _ := body["content"].(string)
	if len(got) != 1<<20 {
		t.Errorf("content length = %d, want %d", len(got), 1<<20)
	}
	if strings.Contains(got, "a") {
		t.Error("content contains bytes from the truncated head of the file")
	}
	if body["truncated"] != true {
		t.Error("large file not reported as truncated")
	}
}

func TestLogAPI_MethodNotAllowed(t *testing.T) {
	svc := logapi.New("")
	rec := doLogRequest(t, svc, http.MethodPost)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}
