package main

import (
	"bytes"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExtractTokens_OpenAIJSON(t *testing.T) {
	body := []byte(`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":32,"total_tokens":42}}`)
	if got := extractTokens("application/json", body); got != 42 {
		t.Errorf("extractTokens = %d, want 42", got)
	}
}

func TestExtractTokens_OllamaJSON(t *testing.T) {
	body := []byte(`{"model":"llama3","prompt_eval_count":15,"eval_count":27}`)
	if got := extractTokens("application/json", body); got != 42 {
		t.Errorf("extractTokens = %d, want 42", got)
	}
}

func TestExtractTokens_SSE(t *testing.T) {
	body := []byte(strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"hi"}}]}`,
		`data: {"choices":[],"usage":{"total_tokens":99}}`,
		`data: [DONE]`,
		``,
	}, "\n"))
	if got := extractTokens("text/event-stream", body); got != 99 {
		t.Errorf("extractTokens = %d, want 99", got)
	}
}

func TestExtractTokens_Unparseable(t *testing.T) {
	if got := extractTokens("application/json", []byte("not json")); got != 0 {
		t.Errorf("extractTokens = %d, want 0", got)
	}
	if got := extractTokens("text/event-stream", []byte("data: nope\n")); got != 0 {
		t.Errorf("extractTokens SSE = %d, want 0", got)
	}
}

func TestExtractPrompt(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "openai string content, last user message wins",
			body: `{"messages":[{"role":"user","content":"first"},{"role":"assistant","content":"a"},{"role":"user","content":"second"}]}`,
			want: "second",
		},
		{
			name: "anthropic content blocks",
			body: `{"messages":[{"role":"user","content":[{"type":"text","text":"block prompt"}]}]}`,
			want: "block prompt",
		},
		{
			name: "system fallback when no user message",
			body: `{"system":"sys prompt","messages":[{"role":"assistant","content":"a"}]}`,
			want: "sys prompt",
		},
		{
			name: "flat prompt field",
			body: `{"prompt":"flat prompt"}`,
			want: "flat prompt",
		},
		{
			name: "non-JSON falls back to raw body",
			body: `plain text`,
			want: "plain text",
		},
		{
			name: "empty body",
			body: "",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractPrompt([]byte(tc.body)); got != tc.want {
				t.Errorf("extractPrompt = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTruncatePrompt(t *testing.T) {
	long := strings.Repeat("x", 3000)
	got := truncatePrompt(long)
	if len(got) != 2000+len("…") {
		t.Errorf("truncated length = %d, want %d", len(got), 2000+len("…"))
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("truncated prompt missing ellipsis suffix")
	}
	if short := truncatePrompt("short"); short != "short" {
		t.Errorf("short prompt mutated: %q", short)
	}
}

func TestLimitedCapture(t *testing.T) {
	var buf bytes.Buffer
	lc := &limitedCapture{w: &buf, max: 10}

	n, err := lc.Write([]byte("0123456789ABCDEF"))
	if err != nil || n != 16 {
		t.Fatalf("Write = (%d, %v), want (16, nil)", n, err)
	}
	if buf.String() != "0123456789" {
		t.Errorf("captured = %q, want first 10 bytes", buf.String())
	}

	// Subsequent writes are discarded but still reported as fully written.
	n, err = lc.Write([]byte("more"))
	if err != nil || n != 4 {
		t.Fatalf("second Write = (%d, %v), want (4, nil)", n, err)
	}
	if buf.Len() != 10 {
		t.Errorf("capture grew past max: %d bytes", buf.Len())
	}
}

func TestTruncateForLog(t *testing.T) {
	if got := truncateForLog([]byte("short"), 10); got != "short" {
		t.Errorf("short input mutated: %q", got)
	}
	got := truncateForLog([]byte("0123456789ABCDEF"), 10)
	if !strings.HasPrefix(got, "0123456789") || !strings.Contains(got, "6 bytes truncated") {
		t.Errorf("truncated output = %q", got)
	}
}

// newTestGateway builds a Gateway without starting its dispatcher, for tests
// that only exercise the tracker/snapshot paths.
func newTestGateway(t *testing.T) *Gateway {
	t.Helper()
	u, _ := url.Parse("http://upstream:11434")
	return &Gateway{
		upstream:      u,
		upstreamModel: "test-model",
		queue:         make(chan *pending, 5),
		priorityQueue: make(chan *pending, 2),
		sem:           make(chan struct{}, 3),
		tracker:       NewTracker(),
	}
}

func TestTracker_RecordAndSnapshot(t *testing.T) {
	g := newTestGateway(t)
	tr := g.tracker

	tr.Record("10.0.0.1", "web-1", "disk-space-check", 100)
	tr.Record("10.0.0.1", "web-1", "disk-space-check", 50)
	tr.Record("10.0.0.2", "db-1", "", 25)

	snap := tr.Snapshot(g)
	if len(snap.Endpoints) != 2 {
		t.Fatalf("endpoints = %d, want 2", len(snap.Endpoints))
	}
	if len(snap.Responsibilities) != 1 {
		t.Fatalf("responsibilities = %d, want 1", len(snap.Responsibilities))
	}

	var web *EndpointStatsSnapshot
	for i := range snap.Endpoints {
		if snap.Endpoints[i].Host == "10.0.0.1" {
			web = &snap.Endpoints[i]
		}
	}
	if web == nil {
		t.Fatal("10.0.0.1 missing from snapshot")
	}
	if web.TokensTotal != 150 || web.RequestsTotal != 2 {
		t.Errorf("web totals = (%d tokens, %d reqs), want (150, 2)", web.TokensTotal, web.RequestsTotal)
	}
	if web.TokensLastHour != 150 || web.RequestsLastHour != 2 {
		t.Errorf("web last hour = (%d tokens, %d reqs), want (150, 2)", web.TokensLastHour, web.RequestsLastHour)
	}
	if web.Name != "web-1" {
		t.Errorf("web name = %q, want web-1", web.Name)
	}

	rs := snap.Responsibilities[0]
	if rs.Name != "disk-space-check" || rs.TokensTotal != 150 || rs.RequestsTotal != 2 {
		t.Errorf("responsibility snapshot = %+v", rs)
	}
	if snap.UpstreamModel != "test-model" {
		t.Errorf("UpstreamModel = %q, want test-model", snap.UpstreamModel)
	}
}

func TestTracker_PendingRegistry(t *testing.T) {
	tr := NewTracker()
	now := time.Now()

	tr.IncrPending("10.0.0.1", "web-1", "cpu-utilization-check", "req-1",
		[]byte(`{"messages":[{"role":"user","content":"check cpu"}]}`), now.Add(-time.Minute), false)
	tr.IncrPending("10.0.0.2", "db-1", "", "req-2", nil, now, true)

	snaps := tr.PendingSnapshot()
	if len(snaps) != 2 {
		t.Fatalf("pending = %d, want 2", len(snaps))
	}
	// Sorted oldest-first.
	if snaps[0].ID != "req-1" || snaps[1].ID != "req-2" {
		t.Errorf("order = [%s, %s], want [req-1, req-2]", snaps[0].ID, snaps[1].ID)
	}
	if snaps[0].Prompt != "check cpu" {
		t.Errorf("prompt = %q, want %q", snaps[0].Prompt, "check cpu")
	}
	if !snaps[1].Priority {
		t.Error("req-2 should be marked priority")
	}

	tr.DecrPending("10.0.0.1", "req-1")
	if got := tr.PendingSnapshot(); len(got) != 1 || got[0].ID != "req-2" {
		t.Errorf("after DecrPending: %+v", got)
	}
}

func TestTracker_Reset(t *testing.T) {
	g := newTestGateway(t)
	tr := g.tracker

	tr.Record("10.0.0.1", "web-1", "disk-space-check", 100)
	tr.Reset()

	snap := tr.Snapshot(g)
	for _, e := range snap.Endpoints {
		if e.TokensTotal != 0 || e.RequestsTotal != 0 || e.TokensLastHour != 0 {
			t.Errorf("endpoint %s not zeroed: %+v", e.Host, e)
		}
	}
	for _, r := range snap.Responsibilities {
		if r.TokensTotal != 0 || r.RequestsTotal != 0 {
			t.Errorf("responsibility %s not zeroed: %+v", r.Name, r)
		}
	}
}

func TestTracker_SaveLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")

	tr := NewTracker()
	tr.Record("10.0.0.1", "web-1", "disk-space-check", 100)
	tr.Record("10.0.0.1", "web-1", "", 50)
	if err := tr.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded := NewTracker()
	if err := loaded.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}

	g := newTestGateway(t)
	g.tracker = loaded
	snap := loaded.Snapshot(g)
	if len(snap.Endpoints) != 1 {
		t.Fatalf("endpoints after load = %d, want 1", len(snap.Endpoints))
	}
	e := snap.Endpoints[0]
	if e.Host != "10.0.0.1" || e.Name != "web-1" || e.TokensTotal != 150 || e.RequestsTotal != 2 {
		t.Errorf("loaded endpoint = %+v", e)
	}
	if len(snap.Responsibilities) != 1 || snap.Responsibilities[0].TokensTotal != 100 {
		t.Errorf("loaded responsibilities = %+v", snap.Responsibilities)
	}
}

func TestTracker_LoadMissingFileIsFreshStart(t *testing.T) {
	tr := NewTracker()
	if err := tr.Load(filepath.Join(t.TempDir(), "does-not-exist.json")); err != nil {
		t.Fatalf("Load of missing file: %v", err)
	}
}
