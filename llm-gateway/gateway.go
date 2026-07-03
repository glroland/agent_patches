package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// nextPendingID is a monotonically-incrementing counter used to assign a unique
// ID to each pending request so it can be tracked in the live pending registry.
var nextPendingID atomic.Int64

// hopByHopHeaders are per-connection headers that must not be forwarded
// to the upstream or returned to the caller (RFC 2616 §13.5.1).
var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailers":            true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

// Gateway is a queuing reverse proxy. Incoming requests are placed on one of
// two bounded FIFO channels: a high-priority channel for interactive (UI-
// initiated) requests, and a normal channel for scheduled background work.
// The dispatcher always drains the priority channel first. A fixed-size
// worker pool limits concurrency. When the applicable queue is full the
// gateway returns 429 immediately rather than blocking the caller.
type Gateway struct {
	upstream      *url.URL
	client        *http.Client
	priorityQueue chan *pending // interactive UI requests — drained first
	queue         chan *pending // scheduled background requests
	sem           chan struct{} // semaphore: len = active requests, cap = max concurrency
	timeout       time.Duration
	dataFile      string
	saveInterval  time.Duration
	tracker       *Tracker

	// ghostNormal/ghostPriority count requests that are still physically in their
	// queue channel but whose client has already disconnected. Used to report
	// accurate effective queue depths in /stats.
	ghostNormal   atomic.Int64
	ghostPriority atomic.Int64
}

// pending carries everything needed to proxy one request. It is enqueued
// by the HTTP handler goroutine and consumed by the dispatcher.
type pending struct {
	ctx            context.Context
	method         string
	path           string
	query          string
	headers        http.Header
	body           []byte
	w              http.ResponseWriter
	done           chan struct{} // closed by forward() when the response is fully written
	host           string        // originating endpoint-server IP for stats tracking
	name           string        // agent display name from X-Agent-Name header
	responsibility string        // scheduled responsibility name from X-Responsibility header; empty for ad-hoc runs
	interactive    bool          // true when X-Priority: interactive; used to route ghost counter

	// cancelled is set by ServeHTTP when p.ctx fires before the request is dispatched.
	// pendingDecremented is a CAS gate ensuring exactly one path calls DecrPending.
	cancelled          atomic.Bool
	pendingDecremented atomic.Bool

	// id uniquely identifies this request in the live pending registry.
	// submittedAt is when the request was placed on the queue.
	id          string
	submittedAt time.Time
}

type healthResponse struct {
	Status                string `json:"status"`
	QueueDepth            int    `json:"queue_depth"`
	QueueCapacity         int    `json:"queue_capacity"`
	PriorityQueueDepth    int    `json:"priority_queue_depth"`
	PriorityQueueCapacity int    `json:"priority_queue_capacity"`
	ActiveRequests        int    `json:"active_requests"`
	MaxConcurrency        int    `json:"max_concurrency"`
	Upstream              string `json:"upstream"`
	GhostRequests         int    `json:"ghost_requests"` // queued requests whose client has disconnected
}

// NewGateway creates a Gateway and starts the background dispatcher goroutine.
// If cfg.DataFile is set, previously persisted stats are loaded before the
// dispatcher starts so token history survives pod restarts.
func NewGateway(cfg Config) (*Gateway, error) {
	u, err := url.Parse(cfg.UpstreamURL)
	if err != nil {
		return nil, fmt.Errorf("invalid GATEWAY_UPSTREAM_URL %q: %w", cfg.UpstreamURL, err)
	}
	g := &Gateway{
		upstream:      u,
		client:        &http.Client{Timeout: cfg.RequestTimeout + 5*time.Second},
		priorityQueue: make(chan *pending, cfg.PriorityQueueDepth),
		queue:         make(chan *pending, cfg.MaxQueueDepth),
		sem:           make(chan struct{}, cfg.MaxConcurrency),
		timeout:       cfg.RequestTimeout,
		dataFile:      cfg.DataFile,
		saveInterval:  cfg.SaveInterval,
		tracker:       NewTracker(),
		// ghostNormal and ghostPriority are zero-value atomic.Int64; no init needed.
	}
	if cfg.DataFile != "" {
		if err := g.tracker.Load(cfg.DataFile); err != nil {
			slog.Warn("gateway: failed to load persisted stats — starting fresh",
				"error", err, "path", cfg.DataFile)
		}
	}
	go g.dispatcher()
	return g, nil
}

// StartPersistence begins the background goroutine that flushes stats to
// cfg.DataFile on a fixed interval and performs a final save when ctx is
// cancelled (clean shutdown). It is a no-op when DataFile is not configured.
func (g *Gateway) StartPersistence(ctx context.Context) {
	if g.dataFile == "" {
		return
	}
	slog.Info("gateway: stats persistence enabled",
		"path", g.dataFile, "interval", g.saveInterval)
	go func() {
		ticker := time.NewTicker(g.saveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := g.tracker.Save(g.dataFile); err != nil {
					slog.Warn("gateway: stats save failed", "error", err)
				} else {
					slog.Debug("gateway: stats saved", "path", g.dataFile)
				}
			case <-ctx.Done():
				if err := g.tracker.Save(g.dataFile); err != nil {
					slog.Warn("gateway: stats final save on shutdown failed", "error", err)
				} else {
					slog.Info("gateway: stats saved on shutdown", "path", g.dataFile)
				}
				return
			}
		}
	}()
}

// ServeHTTP satisfies http.Handler. GET /health and GET /stats are answered
// inline; all other requests are queued for the dispatcher.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		switch r.URL.Path {
		case "/health":
			g.health(w)
			return
		case "/stats":
			g.tracker.statsHandler(g)(w, r)
			return
		case "/pending":
			g.tracker.pendingHandler()(w, r)
			return
		}
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "gateway: read request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	host := clientHost(r)
	interactive := r.Header.Get("X-Priority") == "interactive"
	p := &pending{
		ctx:            r.Context(),
		method:         r.Method,
		path:           r.URL.Path,
		query:          r.URL.RawQuery,
		headers:        r.Header.Clone(),
		body:           body,
		w:              w,
		done:           make(chan struct{}),
		host:           host,
		name:           r.Header.Get("X-Agent-Name"),
		responsibility: r.Header.Get("X-Responsibility"),
		interactive:    interactive,
		id:             fmt.Sprintf("req-%d", nextPendingID.Add(1)),
		submittedAt:    time.Now(),
	}

	// Interactive (UI-initiated) requests go to the priority queue; background
	// scheduled work goes to the normal queue.
	targetQueue := g.queue
	queueLabel := "normal"
	if interactive {
		targetQueue = g.priorityQueue
		queueLabel = "priority"
	}

	select {
	case targetQueue <- p:
		g.tracker.IncrPending(p.host, p.name, p.responsibility, p.id, p.body, p.submittedAt, p.interactive)
		select {
		case <-p.done:
			// forward() completed normally; ResponseWriter was valid throughout.
		case <-p.ctx.Done():
			// Endpoint-server disconnected while the request was queued.
			// Mark cancelled so forward() skips the LLM call when dispatched.
			p.cancelled.Store(true)
			if p.pendingDecremented.CompareAndSwap(false, true) {
				// We won the CAS — correct pending count immediately.
				g.tracker.DecrPending(p.host, p.id)
				// Track the ghost slot so /stats can report accurate queue depths.
				if p.interactive {
					g.ghostPriority.Add(1)
				} else {
					g.ghostNormal.Add(1)
				}
			}
			// CAS loss means forward()'s defer already ran and won — no action needed.
		}
	default:
		slog.Warn("gateway: queue full, returning 429",
			"path", r.URL.Path,
			"host", host,
			"queue", queueLabel,
			"queue_depth", len(targetQueue),
			"queue_capacity", cap(targetQueue),
			"active_requests", len(g.sem),
		)
		w.Header().Set("Retry-After", "5")
		http.Error(w, "gateway: upstream LLM queue is full — retry in a few seconds", http.StatusTooManyRequests)
	}
}

// dispatcher drains both queues, always preferring the priority queue over
// the normal queue. Stage 1 does a non-blocking check of the priority queue
// so that an interactive request already waiting is picked up immediately
// when a concurrency slot opens. Stage 2 blocks on whichever queue has work,
// with the priority queue listed first (select picks randomly when both are
// ready, so interactive requests race ahead of background work on average).
func (g *Gateway) dispatcher() {
	dispatch := func(p *pending) {
		g.sem <- struct{}{} // blocks when max_concurrency slots are occupied
		go func(p *pending) {
			defer func() { <-g.sem }()
			g.forward(p)
		}(p)
	}

	for {
		// Stage 1: non-blocking drain of priority queue.
		select {
		case p := <-g.priorityQueue:
			dispatch(p)
			continue
		default:
		}
		// Stage 2: block until work arrives on either queue.
		select {
		case p := <-g.priorityQueue:
			dispatch(p)
		case p := <-g.queue:
			dispatch(p)
		}
	}
}

// forward executes the upstream request, streams the response back through
// p.w, captures a window of the response body to extract token usage, and
// records the completed request in the tracker. It always closes p.done
// and decrements the pending count before returning.
func (g *Gateway) forward(p *pending) {
	// Snapshot cancellation state before the defer so the defer uses a stable value,
	// avoiding a race where ctx is cancelled mid-execution and would suppress Record.
	wasAlreadyCancelled := p.cancelled.Load()

	var capturedTokens int64
	defer func() {
		// Skip Record for pre-dispatch cancellations — the client is gone and
		// capturedTokens will always be 0, so counting the request is misleading.
		if !wasAlreadyCancelled {
			g.tracker.Record(p.host, p.name, p.responsibility, capturedTokens)
		}
		if p.pendingDecremented.CompareAndSwap(false, true) {
			// Normal completion: ServeHTTP's ctx.Done branch did not fire first.
			g.tracker.DecrPending(p.host, p.id)
		} else {
			// ServeHTTP won the CAS: it already called DecrPending and incremented
			// the ghost counter. The slot is now consumed, so decrement ghost.
			if p.interactive {
				g.ghostPriority.Add(-1)
			} else {
				g.ghostNormal.Add(-1)
			}
		}
		close(p.done)
	}()

	if wasAlreadyCancelled {
		slog.Info("gateway: request abandoned before dispatch — client disconnected",
			"agent", p.name, "responsibility", p.responsibility, "host", p.host)
		return
	}

	target := *g.upstream
	target.Path = p.path
	target.RawQuery = p.query

	ctx, cancel := context.WithTimeout(p.ctx, g.timeout)
	defer cancel()

	upReq, err := http.NewRequestWithContext(ctx, p.method, target.String(), bytes.NewReader(p.body))
	if err != nil {
		slog.Error("gateway: build upstream request", "error", err)
		http.Error(p.w, "gateway: internal error", http.StatusInternalServerError)
		return
	}
	for key, vals := range p.headers {
		if hopByHopHeaders[key] {
			continue
		}
		for _, v := range vals {
			upReq.Header.Add(key, v)
		}
	}

	slog.Debug("gateway: forwarding",
		"method", p.method,
		"path", p.path,
		"host", p.host,
		"queue_depth", len(g.queue),
		"active", len(g.sem),
	)
	slog.Info("gateway: llm request",
		"method", p.method,
		"path", p.path,
		"agent", p.name,
		"responsibility", p.responsibility,
		"body_bytes", len(p.body),
		"body", truncateForLog(p.body, 4096),
	)

	resp, err := g.client.Do(upReq)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			slog.Warn("gateway: upstream request timed out", "path", p.path, "timeout", g.timeout)
			http.Error(p.w, "gateway: upstream timed out", http.StatusGatewayTimeout)
		case errors.Is(err, context.Canceled):
			slog.Info("gateway: upstream request cancelled — client disconnected while queued or in-flight",
				"path", p.path, "agent", p.name, "responsibility", p.responsibility, "host", p.host)
		default:
			slog.Error("gateway: upstream request failed", "path", p.path, "error", err)
			http.Error(p.w, "gateway: upstream error: "+err.Error(), http.StatusBadGateway)
		}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		slog.Error("gateway: LLM returned 429 Too Many Requests — rate limit hit",
			"agent", p.name,
			"responsibility", p.responsibility,
			"path", p.path,
			"host", p.host,
		)
	}

	for key, vals := range resp.Header {
		if hopByHopHeaders[key] {
			continue
		}
		for _, v := range vals {
			p.w.Header().Add(key, v)
		}
	}
	p.w.WriteHeader(resp.StatusCode)

	// Tee the response body into a bounded capture buffer so we can extract
	// token usage stats after forwarding completes. The client receives all
	// bytes in real time regardless of whether the capture limit is hit.
	const maxCaptureBytes = 256 * 1024
	var capBuf bytes.Buffer
	bodyReader := io.TeeReader(resp.Body, &limitedCapture{w: &capBuf, max: maxCaptureBytes})

	flusher, canFlush := p.w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, readErr := bodyReader.Read(buf)
		if n > 0 {
			if _, writeErr := p.w.Write(buf[:n]); writeErr != nil {
				slog.Info("gateway: client disconnected mid-response",
					"path", p.path, "agent", p.name, "responsibility", p.responsibility)
				// capBuf still has partial data — extract what we can.
				capturedTokens = extractTokens(resp.Header.Get("Content-Type"), capBuf.Bytes())
				slog.Info("gateway: llm response (partial — client disconnected)",
					"status", resp.StatusCode,
					"agent", p.name,
					"responsibility", p.responsibility,
					"tokens", capturedTokens,
					"body_bytes", capBuf.Len(),
					"body", truncateForLog(capBuf.Bytes(), 4096),
				)
				return
			}
			if canFlush {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				slog.Warn("gateway: upstream body read error", "path", p.path, "error", readErr)
			}
			break
		}
	}

	capturedTokens = extractTokens(resp.Header.Get("Content-Type"), capBuf.Bytes())
	slog.Info("gateway: llm response",
		"status", resp.StatusCode,
		"agent", p.name,
		"responsibility", p.responsibility,
		"tokens", capturedTokens,
		"body_bytes", capBuf.Len(),
		"body", truncateForLog(capBuf.Bytes(), 4096),
	)
}

func (g *Gateway) health(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(healthResponse{
		Status:                "ok",
		QueueDepth:            len(g.queue),
		QueueCapacity:         cap(g.queue),
		PriorityQueueDepth:    len(g.priorityQueue),
		PriorityQueueCapacity: cap(g.priorityQueue),
		ActiveRequests:        len(g.sem),
		MaxConcurrency:        cap(g.sem),
		Upstream:              g.upstream.String(),
		GhostRequests:         int(g.ghostNormal.Load() + g.ghostPriority.Load()),
	})
}

// truncateForLog returns b as a UTF-8 string, capped at maxLen bytes. If the
// slice is longer a note showing the number of omitted bytes is appended so
// callers can tell the log entry is incomplete.
func truncateForLog(b []byte, maxLen int) string {
	if len(b) <= maxLen {
		return string(b)
	}
	return string(b[:maxLen]) + fmt.Sprintf("... [%d bytes truncated]", len(b)-maxLen)
}

// clientHost returns the originating IP of the request. It checks
// X-Forwarded-For first (set by HAProxy/OpenShift edge router for
// Route-terminated connections) and falls back to RemoteAddr.
func clientHost(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		// X-Forwarded-For may be a comma-separated chain; the leftmost entry
		// is the original client.
		if i := strings.IndexByte(fwd, ','); i != -1 {
			fwd = fwd[:i]
		}
		return strings.TrimSpace(fwd)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
