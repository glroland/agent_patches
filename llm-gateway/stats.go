package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Tracker collects per-endpoint-server token and request statistics using
// a 25-hour sliding window of events. Counters for the last hour and last
// day are derived at query time by scanning the event slice. The total
// pending count (queued + in-flight) is tracked separately via atomics.
//
// All public methods are safe for concurrent use.
type Tracker struct {
	mu        sync.RWMutex
	endpoints map[string]*endpointStats
}

type endpointStats struct {
	mu       sync.Mutex
	host     string
	name     string         // agent display name from X-Agent-Name header
	events   []requestEvent // sorted ascending by at; pruned to 25 h
	total    totalCounters
	pending  atomic.Int64
	lastSeen time.Time
}

type requestEvent struct {
	at     time.Time
	tokens int64 // 0 when token count could not be extracted
}

type totalCounters struct {
	tokens   int64
	requests int64
}

// EndpointStatsSnapshot is the JSON-serialisable view of one endpoint.
type EndpointStatsSnapshot struct {
	Host             string    `json:"host"`
	Name             string    `json:"name,omitempty"`
	PendingRequests  int64     `json:"pending_requests"`
	TokensLastHour   int64     `json:"tokens_last_hour"`
	TokensLastDay    int64     `json:"tokens_last_day"`
	TokensTotal      int64     `json:"tokens_total"`
	RequestsLastHour int64     `json:"requests_last_hour"`
	RequestsLastDay  int64     `json:"requests_last_day"`
	RequestsTotal    int64     `json:"requests_total"`
	LastSeen         time.Time `json:"last_seen"`
}

// GatewayStatsResponse is the top-level payload returned by GET /stats.
type GatewayStatsResponse struct {
	GeneratedAt    time.Time               `json:"generated_at"`
	TotalPending   int                     `json:"total_pending"`
	ActiveRequests int                     `json:"active_requests"`
	QueuedRequests int                     `json:"queued_requests"`
	MaxConcurrency int                     `json:"max_concurrency"`
	QueueCapacity  int                     `json:"queue_capacity"`
	Upstream       string                  `json:"upstream"`
	Endpoints      []EndpointStatsSnapshot `json:"endpoints"`
}

func NewTracker() *Tracker {
	return &Tracker{endpoints: make(map[string]*endpointStats)}
}

func (t *Tracker) get(host, name string) *endpointStats {
	t.mu.RLock()
	es := t.endpoints[host]
	t.mu.RUnlock()
	if es != nil {
		return es
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if es = t.endpoints[host]; es == nil {
		es = &endpointStats{host: host, name: name}
		t.endpoints[host] = es
	}
	return es
}

// IncrPending marks one request as pending for the given host.
func (t *Tracker) IncrPending(host, name string) {
	t.get(host, name).pending.Add(1)
}

// DecrPending marks one request as no longer pending for the given host.
func (t *Tracker) DecrPending(host string) {
	t.get(host, "").pending.Add(-1)
}

// Record appends a completed request to the host's event log. tokens may
// be 0 when the response did not include parseable usage data.
func (t *Tracker) Record(host, name string, tokens int64) {
	es := t.get(host, name)
	es.mu.Lock()
	defer es.mu.Unlock()

	now := time.Now()
	es.events = append(es.events, requestEvent{at: now, tokens: tokens})
	es.total.requests++
	es.total.tokens += tokens
	es.lastSeen = now
	es.prune(now)
}

// prune removes events older than 25 hours. Must be called with es.mu held.
func (es *endpointStats) prune(now time.Time) {
	cutoff := now.Add(-25 * time.Hour)
	i := 0
	for i < len(es.events) && es.events[i].at.Before(cutoff) {
		i++
	}
	if i > 0 {
		es.events = es.events[i:]
	}
}

// windowSums scans es.events in reverse and returns token and request counts
// within the given window. Must be called with es.mu held.
func (es *endpointStats) windowSums(now time.Time, window time.Duration) (tokens, requests int64) {
	for i := len(es.events) - 1; i >= 0; i-- {
		if now.Sub(es.events[i].at) > window {
			break
		}
		tokens += es.events[i].tokens
		requests++
	}
	return
}

// Snapshot returns the current statistics for all known endpoints.
func (t *Tracker) Snapshot(g *Gateway) GatewayStatsResponse {
	now := time.Now()

	t.mu.RLock()
	hosts := make([]string, 0, len(t.endpoints))
	for h := range t.endpoints {
		hosts = append(hosts, h)
	}
	t.mu.RUnlock()

	snaps := make([]EndpointStatsSnapshot, 0, len(hosts))
	for _, h := range hosts {
		es := t.endpoints[h] // safe: entries are never deleted
		es.mu.Lock()
		hourTok, hourReq := es.windowSums(now, time.Hour)
		dayTok, dayReq := es.windowSums(now, 24*time.Hour)
		snap := EndpointStatsSnapshot{
			Host:             h,
			Name:             es.name,
			PendingRequests:  es.pending.Load(),
			TokensLastHour:   hourTok,
			TokensLastDay:    dayTok,
			TokensTotal:      es.total.tokens,
			RequestsLastHour: hourReq,
			RequestsLastDay:  dayReq,
			RequestsTotal:    es.total.requests,
			LastSeen:         es.lastSeen,
		}
		es.mu.Unlock()
		snaps = append(snaps, snap)
	}

	queued := len(g.queue)
	active := len(g.sem)
	return GatewayStatsResponse{
		GeneratedAt:    now,
		TotalPending:   queued + active,
		ActiveRequests: active,
		QueuedRequests: queued,
		MaxConcurrency: cap(g.sem),
		QueueCapacity:  cap(g.queue),
		Upstream:       g.upstream.String(),
		Endpoints:      snaps,
	}
}

// statsHandler is the http.HandlerFunc for GET /stats.
func (t *Tracker) statsHandler(g *Gateway) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(t.Snapshot(g)); err != nil {
			slog.Warn("gateway: write stats response", "error", err)
		}
	}
}

// extractTokens parses usage counters from a buffered response body.
// It handles both OpenAI-style JSON and text/event-stream (SSE) formats,
// including Ollama's native streaming format.
func extractTokens(contentType string, data []byte) int64 {
	if strings.Contains(contentType, "text/event-stream") {
		return extractTokensSSE(data)
	}
	return extractTokensJSON(data)
}

func extractTokensJSON(data []byte) int64 {
	var resp struct {
		Usage struct {
			TotalTokens int64 `json:"total_tokens"`
		} `json:"usage"`
		// Ollama non-streaming format
		PromptEvalCount int64 `json:"prompt_eval_count"`
		EvalCount       int64 `json:"eval_count"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0
	}
	if resp.Usage.TotalTokens > 0 {
		return resp.Usage.TotalTokens
	}
	return resp.PromptEvalCount + resp.EvalCount
}

func extractTokensSSE(data []byte) int64 {
	// Scan SSE data lines in reverse — usage appears in the last data chunk
	// before "data: [DONE]".
	lines := bytes.Split(data, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		line = bytes.TrimPrefix(line, []byte("data: "))
		if !bytes.HasPrefix(line, []byte("{")) {
			continue
		}
		if t := extractTokensJSON(line); t > 0 {
			return t
		}
	}
	return 0
}

// limitedCapture is an io.Writer that fills a bytes.Buffer up to max bytes
// then silently discards the rest. It always reports a successful write to
// callers (including io.TeeReader) so the forwarding loop is never aborted.
type limitedCapture struct {
	w   *bytes.Buffer
	max int
}

func (lc *limitedCapture) Write(p []byte) (int, error) {
	if remaining := lc.max - lc.w.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		lc.w.Write(p) //nolint:errcheck — bytes.Buffer.Write never fails
	}
	return len(p), nil // always report full write to tee reader
}
