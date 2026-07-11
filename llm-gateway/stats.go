package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Tracker collects per-endpoint-server and per-responsibility token and request
// statistics using a 25-hour sliding window of events. Counters for the last
// hour and last day are derived at query time by scanning the event slice. The
// total pending count (queued + in-flight) is tracked separately via atomics.
//
// All public methods are safe for concurrent use.
type Tracker struct {
	mu               sync.RWMutex
	endpoints        map[string]*endpointStats
	responsibilities map[string]*responsibilityStats
	pendingMu        sync.RWMutex
	pendingMap       map[string]*pendingDetail
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

type responsibilityStats struct {
	mu       sync.Mutex
	name     string
	events   []requestEvent // sorted ascending by at; pruned to 25 h
	total    totalCounters
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

// ResponsibilityStatsSnapshot is the JSON-serialisable view of one responsibility.
type ResponsibilityStatsSnapshot struct {
	Name             string    `json:"name"`
	TokensLastHour   int64     `json:"tokens_last_hour"`
	TokensLastDay    int64     `json:"tokens_last_day"`
	TokensTotal      int64     `json:"tokens_total"`
	RequestsLastHour int64     `json:"requests_last_hour"`
	RequestsLastDay  int64     `json:"requests_last_day"`
	RequestsTotal    int64     `json:"requests_total"`
	LastSeen         time.Time `json:"last_seen"`
}

// pendingDetail holds the live detail of one in-flight or queued request.
type pendingDetail struct {
	host           string
	name           string
	responsibility string
	prompt         string
	submittedAt    time.Time
	priority       bool
}

// PendingRequestSnapshot is the JSON-serialisable view of one pending request.
type PendingRequestSnapshot struct {
	ID             string    `json:"id"`
	Host           string    `json:"host"`
	Name           string    `json:"name,omitempty"`
	Responsibility string    `json:"responsibility,omitempty"`
	Prompt         string    `json:"prompt"`
	SubmittedAt    time.Time `json:"submitted_at"`
	Priority       bool      `json:"priority"`
}

// GatewayStatsResponse is the top-level payload returned by GET /stats.
type GatewayStatsResponse struct {
	GeneratedAt            time.Time                     `json:"generated_at"`
	TotalPending           int                           `json:"total_pending"`
	ActiveRequests         int                           `json:"active_requests"`
	QueuedRequests         int                           `json:"queued_requests"`
	PriorityQueuedRequests int                           `json:"priority_queued_requests"`
	GhostRequests          int                           `json:"ghost_requests"` // queued slots whose client has disconnected (excluded from Queued* counts above)
	MaxConcurrency         int                           `json:"max_concurrency"`
	QueueCapacity          int                           `json:"queue_capacity"`
	PriorityQueueCapacity  int                           `json:"priority_queue_capacity"`
	Upstream               string                        `json:"upstream"`
	UpstreamModel          string                        `json:"upstream_model,omitempty"`
	Endpoints              []EndpointStatsSnapshot       `json:"endpoints"`
	Responsibilities       []ResponsibilityStatsSnapshot `json:"responsibilities"`
}

func NewTracker() *Tracker {
	return &Tracker{
		endpoints:        make(map[string]*endpointStats),
		responsibilities: make(map[string]*responsibilityStats),
		pendingMap:       make(map[string]*pendingDetail),
	}
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

func (t *Tracker) getResponsibility(name string) *responsibilityStats {
	t.mu.RLock()
	rs := t.responsibilities[name]
	t.mu.RUnlock()
	if rs != nil {
		return rs
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if rs = t.responsibilities[name]; rs == nil {
		rs = &responsibilityStats{name: name}
		t.responsibilities[name] = rs
	}
	return rs
}

// IncrPending marks one request as pending for the given host and stores its
// details in the live pending registry so they can be surfaced via GET /pending.
func (t *Tracker) IncrPending(host, name, responsibility, id string, body []byte, submittedAt time.Time, priority bool) {
	t.get(host, name).pending.Add(1)
	t.pendingMu.Lock()
	t.pendingMap[id] = &pendingDetail{
		host:           host,
		name:           name,
		responsibility: responsibility,
		prompt:         extractPrompt(body),
		submittedAt:    submittedAt,
		priority:       priority,
	}
	t.pendingMu.Unlock()
}

// DecrPending marks one request as no longer pending for the given host and
// removes it from the live pending registry.
func (t *Tracker) DecrPending(host, id string) {
	t.get(host, "").pending.Add(-1)
	t.pendingMu.Lock()
	delete(t.pendingMap, id)
	t.pendingMu.Unlock()
}

// Record appends a completed request to the host's event log and, when a
// responsibility name is provided, to the responsibility's event log as well.
// tokens may be 0 when the response did not include parseable usage data.
func (t *Tracker) Record(host, name, responsibility string, tokens int64) {
	now := time.Now()

	es := t.get(host, name)
	es.mu.Lock()
	es.events = append(es.events, requestEvent{at: now, tokens: tokens})
	es.total.requests++
	es.total.tokens += tokens
	es.lastSeen = now
	es.prune(now)
	es.mu.Unlock()

	if responsibility == "" {
		return
	}
	rs := t.getResponsibility(responsibility)
	rs.mu.Lock()
	rs.events = append(rs.events, requestEvent{at: now, tokens: tokens})
	rs.total.requests++
	rs.total.tokens += tokens
	rs.lastSeen = now
	rs.prune(now)
	rs.mu.Unlock()
}

// prune removes events older than 25 hours from a responsibilityStats.
// Must be called with rs.mu held.
func (rs *responsibilityStats) prune(now time.Time) {
	cutoff := now.Add(-25 * time.Hour)
	i := 0
	for i < len(rs.events) && rs.events[i].at.Before(cutoff) {
		i++
	}
	if i > 0 {
		rs.events = rs.events[i:]
	}
}

// windowSums scans rs.events in reverse and returns token and request counts
// within the given window. Must be called with rs.mu held.
func (rs *responsibilityStats) windowSums(now time.Time, window time.Duration) (tokens, requests int64) {
	for i := len(rs.events) - 1; i >= 0; i-- {
		if now.Sub(rs.events[i].at) > window {
			break
		}
		tokens += rs.events[i].tokens
		requests++
	}
	return
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

// Snapshot returns the current statistics for all known endpoints and responsibilities.
func (t *Tracker) Snapshot(g *Gateway) GatewayStatsResponse {
	now := time.Now()

	t.mu.RLock()
	hosts := make([]string, 0, len(t.endpoints))
	for h := range t.endpoints {
		hosts = append(hosts, h)
	}
	respNames := make([]string, 0, len(t.responsibilities))
	for n := range t.responsibilities {
		respNames = append(respNames, n)
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

	respSnaps := make([]ResponsibilityStatsSnapshot, 0, len(respNames))
	for _, n := range respNames {
		rs := t.responsibilities[n] // safe: entries are never deleted
		rs.mu.Lock()
		hourTok, hourReq := rs.windowSums(now, time.Hour)
		dayTok, dayReq := rs.windowSums(now, 24*time.Hour)
		snap := ResponsibilityStatsSnapshot{
			Name:             n,
			TokensLastHour:   hourTok,
			TokensLastDay:    dayTok,
			TokensTotal:      rs.total.tokens,
			RequestsLastHour: hourReq,
			RequestsLastDay:  dayReq,
			RequestsTotal:    rs.total.requests,
			LastSeen:         rs.lastSeen,
		}
		rs.mu.Unlock()
		respSnaps = append(respSnaps, snap)
	}

	// Subtract ghost slots (queued but client disconnected) to report effective depths.
	// max(0,...) guards against a transient overread between two separate atomics.
	ghostN := int(g.ghostNormal.Load())
	ghostP := int(g.ghostPriority.Load())
	queued := max(0, len(g.queue)-ghostN)
	priorityQueued := max(0, len(g.priorityQueue)-ghostP)
	active := len(g.sem)
	return GatewayStatsResponse{
		GeneratedAt:            now,
		TotalPending:           queued + priorityQueued + active,
		ActiveRequests:         active,
		QueuedRequests:         queued,
		PriorityQueuedRequests: priorityQueued,
		GhostRequests:          ghostN + ghostP,
		MaxConcurrency:         cap(g.sem),
		QueueCapacity:          cap(g.queue),
		PriorityQueueCapacity:  cap(g.priorityQueue),
		Upstream:               g.upstream.String(),
		UpstreamModel:          g.upstreamModel,
		Endpoints:              snaps,
		Responsibilities:       respSnaps,
	}
}

// PendingSnapshot returns a list of all currently pending requests sorted
// oldest-first. The returned slice is a copy; the caller may freely mutate it.
func (t *Tracker) PendingSnapshot() []PendingRequestSnapshot {
	t.pendingMu.RLock()
	snaps := make([]PendingRequestSnapshot, 0, len(t.pendingMap))
	for id, d := range t.pendingMap {
		snaps = append(snaps, PendingRequestSnapshot{
			ID:             id,
			Host:           d.host,
			Name:           d.name,
			Responsibility: d.responsibility,
			Prompt:         d.prompt,
			SubmittedAt:    d.submittedAt,
			Priority:       d.priority,
		})
	}
	t.pendingMu.RUnlock()
	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].SubmittedAt.Before(snaps[j].SubmittedAt)
	})
	return snaps
}

// pendingHandler is the http.HandlerFunc for GET /pending.
func (t *Tracker) pendingHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := struct {
			GeneratedAt time.Time                `json:"generated_at"`
			Requests    []PendingRequestSnapshot `json:"requests"`
		}{
			GeneratedAt: time.Now(),
			Requests:    t.PendingSnapshot(),
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			slog.Warn("gateway: write pending response", "error", err)
		}
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

// Reset zeroes out all accumulated token/request statistics for every known
// endpoint and responsibility. Existing map entries (and their live `pending`
// counters) are left in place rather than deleted, so requests already queued
// or in flight when Reset is called are still decremented correctly by
// DecrPending afterwards.
func (t *Tracker) Reset() {
	t.mu.RLock()
	hosts := make([]string, 0, len(t.endpoints))
	for h := range t.endpoints {
		hosts = append(hosts, h)
	}
	respNames := make([]string, 0, len(t.responsibilities))
	for n := range t.responsibilities {
		respNames = append(respNames, n)
	}
	t.mu.RUnlock()

	for _, h := range hosts {
		es := t.endpoints[h] // safe: entries are never deleted
		es.mu.Lock()
		es.events = nil
		es.total = totalCounters{}
		es.lastSeen = time.Time{}
		es.mu.Unlock()
	}
	for _, n := range respNames {
		rs := t.responsibilities[n] // safe: entries are never deleted
		rs.mu.Lock()
		rs.events = nil
		rs.total = totalCounters{}
		rs.lastSeen = time.Time{}
		rs.mu.Unlock()
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

// extractPrompt attempts to pull a human-readable prompt string from a raw LLM
// request body. It handles Anthropic messages format (content as string or
// content-block array), OpenAI messages format, and flat prompt fields.
// Falls back to a truncated copy of the raw body when the JSON cannot be parsed.
func extractPrompt(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var req struct {
		System   string `json:"system"`
		Prompt   string `json:"prompt"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err == nil {
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role != "user" {
				continue
			}
			var s string
			if err := json.Unmarshal(req.Messages[i].Content, &s); err == nil {
				return truncatePrompt(s)
			}
			var blocks []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal(req.Messages[i].Content, &blocks); err == nil {
				for _, b := range blocks {
					if b.Type == "text" && b.Text != "" {
						return truncatePrompt(b.Text)
					}
				}
			}
		}
		if req.System != "" {
			return truncatePrompt(req.System)
		}
		if req.Prompt != "" {
			return truncatePrompt(req.Prompt)
		}
	}
	return truncatePrompt(string(body))
}

func truncatePrompt(s string) string {
	const maxLen = 2000
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

// limitedCapture is an io.Writer that fills a bytes.Buffer up to max bytes
// then silently discards the rest. It always reports a successful write to
// callers (including io.TeeReader) so the forwarding loop is never aborted.
type limitedCapture struct {
	w   *bytes.Buffer
	max int
}

func (lc *limitedCapture) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := lc.max - lc.w.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		lc.w.Write(p) //nolint:errcheck — bytes.Buffer.Write never fails
	}
	return n, nil // always report full write so the tee reader never aborts the stream
}

// ---------------------------------------------------------------------------
// Persistence — JSON file on a PVC mount
// ---------------------------------------------------------------------------

const persistenceVersion = 1

type persistedState struct {
	Version          int                       `json:"version"`
	SavedAt          time.Time                 `json:"saved_at"`
	Endpoints        []persistedEndpoint       `json:"endpoints"`
	Responsibilities []persistedResponsibility `json:"responsibilities"`
}

type persistedEndpoint struct {
	Host          string           `json:"host"`
	Name          string           `json:"name,omitempty"`
	LastSeen      time.Time        `json:"last_seen"`
	TotalTokens   int64            `json:"total_tokens"`
	TotalRequests int64            `json:"total_requests"`
	Events        []persistedEvent `json:"events"`
}

type persistedResponsibility struct {
	Name          string           `json:"name"`
	LastSeen      time.Time        `json:"last_seen"`
	TotalTokens   int64            `json:"total_tokens"`
	TotalRequests int64            `json:"total_requests"`
	Events        []persistedEvent `json:"events"`
}

type persistedEvent struct {
	At     time.Time `json:"at"`
	Tokens int64     `json:"tokens"`
}

// Load reads the persisted stats file at path and populates the Tracker.
// A missing file is treated as a fresh start. A corrupt or version-mismatched
// file logs a warning and leaves the Tracker empty rather than aborting startup.
// Load must be called before the gateway begins accepting requests (no locking
// is needed during load itself).
func (t *Tracker) Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read stats file: %w", err)
	}

	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("parse stats file: %w", err)
	}
	if state.Version != persistenceVersion {
		return fmt.Errorf("unsupported stats file version %d (want %d)", state.Version, persistenceVersion)
	}

	cutoff := time.Now().Add(-25 * time.Hour)

	for _, pe := range state.Endpoints {
		es := &endpointStats{
			host:     pe.Host,
			name:     pe.Name,
			lastSeen: pe.LastSeen,
			total:    totalCounters{tokens: pe.TotalTokens, requests: pe.TotalRequests},
		}
		for _, e := range pe.Events {
			if !e.At.Before(cutoff) {
				es.events = append(es.events, requestEvent{at: e.At, tokens: e.Tokens})
			}
		}
		t.endpoints[pe.Host] = es
	}

	for _, pr := range state.Responsibilities {
		rs := &responsibilityStats{
			name:     pr.Name,
			lastSeen: pr.LastSeen,
			total:    totalCounters{tokens: pr.TotalTokens, requests: pr.TotalRequests},
		}
		for _, e := range pr.Events {
			if !e.At.Before(cutoff) {
				rs.events = append(rs.events, requestEvent{at: e.At, tokens: e.Tokens})
			}
		}
		t.responsibilities[pr.Name] = rs
	}

	slog.Info("gateway: loaded persisted stats",
		"path", path,
		"endpoints", len(state.Endpoints),
		"responsibilities", len(state.Responsibilities),
		"saved_at", state.SavedAt,
	)
	return nil
}

// Save atomically writes the current Tracker state to path. It writes to a
// temporary file alongside path then renames it into place, which is atomic
// on Linux filesystems (ext4, xfs) used by PVC mounts.
func (t *Tracker) Save(path string) error {
	// Snapshot map keys under the read lock; entries are never deleted so
	// dereferencing the pointers afterwards is safe without re-locking.
	t.mu.RLock()
	hosts := make([]string, 0, len(t.endpoints))
	for h := range t.endpoints {
		hosts = append(hosts, h)
	}
	respNames := make([]string, 0, len(t.responsibilities))
	for n := range t.responsibilities {
		respNames = append(respNames, n)
	}
	t.mu.RUnlock()

	// Sort for deterministic output so diff-based monitoring stays quiet.
	sort.Strings(hosts)
	sort.Strings(respNames)

	state := persistedState{
		Version:          persistenceVersion,
		SavedAt:          time.Now(),
		Endpoints:        make([]persistedEndpoint, 0, len(hosts)),
		Responsibilities: make([]persistedResponsibility, 0, len(respNames)),
	}

	for _, h := range hosts {
		es := t.endpoints[h]
		es.mu.Lock()
		pe := persistedEndpoint{
			Host:          es.host,
			Name:          es.name,
			LastSeen:      es.lastSeen,
			TotalTokens:   es.total.tokens,
			TotalRequests: es.total.requests,
			Events:        make([]persistedEvent, len(es.events)),
		}
		for i, e := range es.events {
			pe.Events[i] = persistedEvent{At: e.at, Tokens: e.tokens}
		}
		es.mu.Unlock()
		state.Endpoints = append(state.Endpoints, pe)
	}

	for _, n := range respNames {
		rs := t.responsibilities[n]
		rs.mu.Lock()
		pr := persistedResponsibility{
			Name:          rs.name,
			LastSeen:      rs.lastSeen,
			TotalTokens:   rs.total.tokens,
			TotalRequests: rs.total.requests,
			Events:        make([]persistedEvent, len(rs.events)),
		}
		for i, e := range rs.events {
			pr.Events[i] = persistedEvent{At: e.at, Tokens: e.tokens}
		}
		rs.mu.Unlock()
		state.Responsibilities = append(state.Responsibilities, pr)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal stats: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write stats temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename stats file: %w", err)
	}
	return nil
}
