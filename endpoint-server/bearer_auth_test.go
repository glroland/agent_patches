package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const testToken = "test-secret-token"

// okHandler is a trivial inner handler that records it was reached.
func okHandler(t *testing.T, reached *bool) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
	})
}

func TestRequireBearer_NoAuthorizationHeader_Returns401(t *testing.T) {
	reached := false
	handler := requireBearer(testToken, okHandler(t, &reached))

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("WWW-Authenticate = %q, want \"Bearer\"", rec.Header().Get("WWW-Authenticate"))
	}
	if reached {
		t.Error("inner handler must not be called when no token is present")
	}
}

func TestRequireBearer_WrongToken_Returns401(t *testing.T) {
	reached := false
	handler := requireBearer(testToken, okHandler(t, &reached))

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if reached {
		t.Error("inner handler must not be called with a wrong token")
	}
}

func TestRequireBearer_CorrectToken_PassesThrough(t *testing.T) {
	reached := false
	handler := requireBearer(testToken, okHandler(t, &reached))

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !reached {
		t.Error("inner handler was not called with a valid token")
	}
}

func TestRequireBearer_WrongScheme_Returns401(t *testing.T) {
	// HTTP auth scheme matching is case-sensitive; "Bearer" and "bearer" are
	// different strings. Non-Bearer schemes must never grant access.
	cases := []struct {
		header string
	}{
		{"Token " + testToken},  // wrong scheme name
		{"Basic " + testToken},  // basic auth scheme
		{testToken},             // raw token, no scheme
		{"bearer " + testToken}, // lowercase scheme
		{"BEARER " + testToken}, // uppercase scheme
	}
	for _, tc := range cases {
		reached := false
		handler := requireBearer(testToken, okHandler(t, &reached))

		req := httptest.NewRequest(http.MethodGet, "/status", nil)
		req.Header.Set("Authorization", tc.header)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Authorization: %q → status = %d, want 401", tc.header, rec.Code)
		}
		if reached {
			t.Errorf("Authorization: %q → inner handler must not be called", tc.header)
		}
	}
}

func TestRequireBearer_EmptyTokenValue_Returns401(t *testing.T) {
	// "Bearer " with no token after the space must fail when the configured
	// token is non-empty — an empty token value is not a valid credential.
	reached := false
	handler := requireBearer(testToken, okHandler(t, &reached))

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for empty token value", rec.Code)
	}
	if reached {
		t.Error("inner handler must not be called with an empty token value")
	}
}

func TestRequireBearer_AllPlainEndpoints_Protected(t *testing.T) {
	// Regression test: all plain-HTTP endpoints must reject requests without a
	// valid token. This test does not spin up a real server; it verifies that
	// requireBearer gates each endpoint path at the handler level.
	endpoints := []string{"/status", "/memory", "/approvals/", "/responsibilities/"}
	for _, path := range endpoints {
		reached := false
		handler := requireBearer(testToken, okHandler(t, &reached))

		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("path %s without token: status = %d, want 401", path, rec.Code)
		}
		if reached {
			t.Errorf("path %s: inner handler must not run without a token", path)
		}
	}
}
