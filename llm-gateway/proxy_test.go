package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRequireBearer(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cases := []struct {
		name     string
		token    string
		path     string
		header   string
		wantCode int
	}{
		{"health always open", "secret", "/health", "", http.StatusOK},
		{"empty token disables auth", "", "/v1/chat/completions", "", http.StatusOK},
		{"missing header rejected", "secret", "/v1/chat/completions", "", http.StatusUnauthorized},
		{"wrong token rejected", "secret", "/v1/chat/completions", "Bearer wrong", http.StatusUnauthorized},
		{"malformed scheme rejected", "secret", "/v1/chat/completions", "Basic secret", http.StatusUnauthorized},
		{"valid token accepted", "secret", "/v1/chat/completions", "Bearer secret", http.StatusOK},
		{"stats requires auth", "secret", "/stats", "", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			requireBearer(tc.token, next).ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantCode)
			}
		})
	}
}

func TestClientHost(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{"remote addr with port", "10.1.2.3:54321", "", "10.1.2.3"},
		{"xff single", "10.1.2.3:54321", "192.168.9.9", "192.168.9.9"},
		{"xff chain takes leftmost", "10.1.2.3:54321", "192.168.9.9, 10.0.0.1", "192.168.9.9"},
		{"xff with spaces", "10.1.2.3:54321", "  192.168.9.9  ", "192.168.9.9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := clientHost(req); got != tc.want {
				t.Errorf("clientHost = %q, want %q", got, tc.want)
			}
		})
	}
}

// newProxyGateway builds a running gateway (dispatcher started) pointed at
// the given upstream URL.
func newProxyGateway(t *testing.T, upstreamURL string, cfgMod func(*Config)) *Gateway {
	t.Helper()
	cfg := Config{
		ListenAddr:         ":0",
		UpstreamURL:        upstreamURL,
		UpstreamModel:      "fleet-default-model",
		MaxConcurrency:     2,
		MaxQueueDepth:      4,
		PriorityQueueDepth: 2,
		RequestTimeout:     5 * time.Second,
	}
	if cfgMod != nil {
		cfgMod(&cfg)
	}
	g, err := NewGateway(cfg)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	return g
}

func TestGateway_ForwardsAndRecordsStats(t *testing.T) {
	var gotBody []byte
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[],"usage":{"total_tokens":42}}`))
	}))
	defer upstream.Close()

	g := newProxyGateway(t, upstream.URL, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"DEFAULT","messages":[]}`))
	req.RemoteAddr = "10.9.8.7:1234"
	req.Header.Set("X-Agent-Name", "web-1")
	req.Header.Set("X-Responsibility", "disk-space-check")
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("upstream path = %q", gotPath)
	}
	// The DEFAULT sentinel must be rewritten to the configured upstream model.
	var sent map[string]any
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("upstream body not JSON: %v", err)
	}
	if sent["model"] != "fleet-default-model" {
		t.Errorf("upstream model = %v, want fleet-default-model", sent["model"])
	}
	if !strings.Contains(rec.Body.String(), `"total_tokens":42`) {
		t.Errorf("response body not forwarded: %s", rec.Body)
	}

	snap := g.tracker.Snapshot(g)
	if len(snap.Endpoints) != 1 || snap.Endpoints[0].Host != "10.9.8.7" {
		t.Fatalf("endpoints = %+v", snap.Endpoints)
	}
	if snap.Endpoints[0].TokensTotal != 42 {
		t.Errorf("tokens = %d, want 42", snap.Endpoints[0].TokensTotal)
	}
	if len(snap.Responsibilities) != 1 || snap.Responsibilities[0].Name != "disk-space-check" {
		t.Errorf("responsibilities = %+v", snap.Responsibilities)
	}
	if snap.Endpoints[0].PendingRequests != 0 {
		t.Errorf("pending after completion = %d, want 0", snap.Endpoints[0].PendingRequests)
	}
}

func TestGateway_HealthAndStatsEndpoints(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	g := newProxyGateway(t, upstream.URL, nil)

	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health = %d", rec.Code)
	}
	var health healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
		t.Fatalf("health body: %v", err)
	}
	if health.Status != "ok" || health.MaxConcurrency != 2 || health.QueueCapacity != 4 {
		t.Errorf("health = %+v", health)
	}

	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stats", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /stats = %d", rec.Code)
	}
	var stats GatewayStatsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatalf("stats body: %v", err)
	}
	if stats.UpstreamModel != "fleet-default-model" {
		t.Errorf("stats.UpstreamModel = %q", stats.UpstreamModel)
	}

	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/pending", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /pending = %d", rec.Code)
	}
}

func TestGateway_ResetStats(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"usage":{"total_tokens":10}}`))
	}))
	defer upstream.Close()
	g := newProxyGateway(t, upstream.URL, nil)

	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("proxy request = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/stats", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE /stats = %d, body = %s", rec.Code, rec.Body)
	}

	snap := g.tracker.Snapshot(g)
	for _, e := range snap.Endpoints {
		if e.TokensTotal != 0 || e.RequestsTotal != 0 {
			t.Errorf("stats not reset: %+v", e)
		}
	}
}

func TestGateway_QueueFullReturns429(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-release
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()
	defer close(release)

	g := newProxyGateway(t, upstream.URL, func(c *Config) {
		c.MaxConcurrency = 1
		c.MaxQueueDepth = 1
		c.PriorityQueueDepth = 1
	})

	// First request occupies the single concurrency slot.
	go func() {
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(`{}`)))
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first request never reached upstream")
	}

	// Second request fills the normal queue (dispatcher is blocked on the sem).
	go func() {
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(`{}`)))
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(g.queue) < 1 {
		if time.Now().After(deadline) {
			t.Fatal("second request never queued")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Third request finds the queue full and must fail fast with 429.
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(`{}`)))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("429 response missing Retry-After header")
	}

	// The priority queue is separate: an interactive request still gets in.
	go func() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(`{}`))
		req.Header.Set("X-Priority", "interactive")
		g.ServeHTTP(rec, req)
	}()
	deadline = time.Now().Add(5 * time.Second)
	for len(g.priorityQueue) < 1 {
		if time.Now().After(deadline) {
			t.Fatal("interactive request never queued despite free priority queue")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// failingWriter is an http.ResponseWriter whose Write always fails, simulating
// a client that disconnects while the gateway is streaming a response back to it.
type failingWriter struct {
	header http.Header
}

func (f *failingWriter) Header() http.Header {
	if f.header == nil {
		f.header = make(http.Header)
	}
	return f.header
}
func (f *failingWriter) WriteHeader(int) {}
func (f *failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("broken pipe")
}

func newForwardPending(ctx context.Context, w http.ResponseWriter, host, id string) *pending {
	return &pending{
		ctx:    ctx,
		method: http.MethodPost,
		path:   "/v1/x",
		w:      w,
		done:   make(chan struct{}),
		host:   host,
		id:     id,
	}
}

func TestGateway_Forward_IncomingConnError_PreDispatchCancel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	g := newProxyGateway(t, upstream.URL, nil)

	p := newForwardPending(context.Background(), httptest.NewRecorder(), "10.0.0.1", "req-1")
	p.cancelled.Store(true)

	g.forward(p)
	<-p.done

	snap := g.tracker.Snapshot(g)
	if snap.IncomingConnErrorsTotal != 1 {
		t.Errorf("IncomingConnErrorsTotal = %d, want 1", snap.IncomingConnErrorsTotal)
	}
	if snap.OutgoingConnErrorsTotal != 0 {
		t.Errorf("OutgoingConnErrorsTotal = %d, want 0", snap.OutgoingConnErrorsTotal)
	}
}

func TestGateway_Forward_IncomingConnError_MidFlightCancel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	g := newProxyGateway(t, upstream.URL, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := newForwardPending(ctx, httptest.NewRecorder(), "10.0.0.2", "req-2")

	g.forward(p)
	<-p.done

	snap := g.tracker.Snapshot(g)
	if snap.IncomingConnErrorsTotal != 1 {
		t.Errorf("IncomingConnErrorsTotal = %d, want 1", snap.IncomingConnErrorsTotal)
	}
	if snap.OutgoingConnErrorsTotal != 0 {
		t.Errorf("OutgoingConnErrorsTotal = %d, want 0", snap.OutgoingConnErrorsTotal)
	}
}

func TestGateway_Forward_OutgoingConnError_Timeout(t *testing.T) {
	block := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer upstream.Close()
	defer close(block)

	g := newProxyGateway(t, upstream.URL, func(c *Config) {
		c.RequestTimeout = 20 * time.Millisecond
	})
	p := newForwardPending(context.Background(), httptest.NewRecorder(), "10.0.0.3", "req-3")

	g.forward(p)
	<-p.done

	snap := g.tracker.Snapshot(g)
	if snap.OutgoingConnErrorsTotal != 1 {
		t.Errorf("OutgoingConnErrorsTotal = %d, want 1", snap.OutgoingConnErrorsTotal)
	}
	if snap.IncomingConnErrorsTotal != 0 {
		t.Errorf("IncomingConnErrorsTotal = %d, want 0", snap.IncomingConnErrorsTotal)
	}
}

func TestGateway_Forward_OutgoingConnError_NetworkFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	upstreamURL := upstream.URL
	upstream.Close() // nothing listening now — Do() will fail to connect

	g := newProxyGateway(t, upstreamURL, nil)
	p := newForwardPending(context.Background(), httptest.NewRecorder(), "10.0.0.4", "req-4")

	g.forward(p)
	<-p.done

	snap := g.tracker.Snapshot(g)
	if snap.OutgoingConnErrorsTotal != 1 {
		t.Errorf("OutgoingConnErrorsTotal = %d, want 1", snap.OutgoingConnErrorsTotal)
	}
	if snap.IncomingConnErrorsTotal != 0 {
		t.Errorf("IncomingConnErrorsTotal = %d, want 0", snap.IncomingConnErrorsTotal)
	}
}

func TestGateway_Forward_IncomingConnError_MidResponseWriteFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("some response body"))
	}))
	defer upstream.Close()
	g := newProxyGateway(t, upstream.URL, nil)

	p := newForwardPending(context.Background(), &failingWriter{}, "10.0.0.5", "req-5")

	g.forward(p)
	<-p.done

	snap := g.tracker.Snapshot(g)
	if snap.IncomingConnErrorsTotal != 1 {
		t.Errorf("IncomingConnErrorsTotal = %d, want 1", snap.IncomingConnErrorsTotal)
	}
	if snap.OutgoingConnErrorsTotal != 0 {
		t.Errorf("OutgoingConnErrorsTotal = %d, want 0", snap.OutgoingConnErrorsTotal)
	}
}
