package status

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"

	"agent_patches/endpoint-server/utils/config"
)

type headerTransport struct {
	inner http.RoundTripper
	name  string
}

func (t *headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("X-Agent-Name", t.name)
	return t.inner.RoundTrip(r)
}

const (
	defaultSummaryTTL = 5 * time.Minute
	summaryTimeout    = 30 * time.Second
	summaryMaxTok     = 80
)

const summarySystemPrompt = `You are a concise system status reporter for a server agent.
Given a snapshot of the agent's current health issues and pending actions, write a single
sentence (max 25 words) that tells the operator exactly what needs their attention.
Be specific — name the actual issue, not vague phrases like "needs review".
No preamble. No trailing punctuation.`

// summarizer generates and caches an AI status description for the "attention"
// state. The cache is invalidated when the alert content changes (detected via
// a SHA-256 hash of the prompt) or when the configurable TTL elapses, whichever
// comes first.
type summarizer struct {
	client     openai.Client
	model      string
	ttl        time.Duration
	mu         sync.Mutex
	cached     string
	cachedHash string
	cachedAt   time.Time
	running    bool
}

func newSummarizer(cfg *config.Settings) *summarizer {
	ttl := defaultSummaryTTL
	if cfg.Status.SummaryTTL != "" {
		if d, err := time.ParseDuration(cfg.Status.SummaryTTL); err == nil && d > 0 {
			ttl = d
		} else {
			slog.Warn("status: invalid summary_ttl, using default", "value", cfg.Status.SummaryTTL, "default", ttl)
		}
	}
	opts := []option.RequestOption{
		option.WithHTTPClient(&http.Client{
			Transport: &headerTransport{
				inner: http.DefaultTransport,
				name:  cfg.Server.Name,
			},
		}),
	}
	if cfg.Agent.APIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.Agent.APIKey))
	}
	if cfg.Agent.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.Agent.BaseURL))
	}
	return &summarizer{
		client: openai.NewClient(opts...),
		model:  cfg.Agent.Model,
		ttl:    ttl,
	}
}

// get returns the latest cached summary. If the alert content has changed or
// the TTL has elapsed, a background refresh is spawned. Callers receive the
// previous result instantly while the model generates the new one.
func (s *summarizer) get(timeline []TimelineEntry) string {
	prompt := buildSummaryPrompt(timeline)
	if prompt == "" {
		return ""
	}
	hash := promptHash(prompt)

	s.mu.Lock()
	cached := s.cached
	stale := time.Since(s.cachedAt) >= s.ttl || hash != s.cachedHash
	running := s.running
	s.mu.Unlock()

	if stale && !running {
		s.mu.Lock()
		if !s.running {
			s.running = true
			go s.refresh(prompt, hash)
		}
		s.mu.Unlock()
	}

	return cached
}

func (s *summarizer) refresh(prompt, hash string) {
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), summaryTimeout)
	defer cancel()

	resp, err := s.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:     s.model,
		MaxTokens: param.NewOpt(int64(summaryMaxTok)),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(summarySystemPrompt),
			openai.UserMessage(prompt),
		},
	})
	if err != nil {
		slog.Warn("status: AI summary failed", "error", err)
		return
	}
	if len(resp.Choices) == 0 {
		return
	}

	summary := strings.TrimSpace(resp.Choices[0].Message.Content)
	if summary == "" {
		return
	}

	s.mu.Lock()
	s.cached = summary
	s.cachedHash = hash
	s.cachedAt = time.Now()
	s.mu.Unlock()
	slog.Debug("status: AI summary updated", "summary", summary)
}

// promptHash returns a short hex fingerprint of the prompt string.
func promptHash(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return fmt.Sprintf("%x", sum)
}

// buildSummaryPrompt assembles the health snapshot sent to the model.
// Only attention-driving entries are included to keep the context tight.
func buildSummaryPrompt(timeline []TimelineEntry) string {
	var sb strings.Builder

	escalations := filterEntries(timeline, func(e TimelineEntry) bool {
		return e.Type == "escalation"
	})
	criticals := filterEntries(timeline, func(e TimelineEntry) bool {
		return e.Severity == "critical" && e.Type != "escalation"
	})
	approvals := filterEntries(timeline, func(e TimelineEntry) bool {
		return e.Type == "approval" && e.Status != nil && *e.Status == "pending" &&
			(e.Risk == "high" || e.Risk == "medium")
	})

	if len(escalations)+len(criticals)+len(approvals) == 0 {
		return ""
	}

	if len(escalations) > 0 {
		sb.WriteString("ESCALATIONS (approval not acknowledged by operator):\n")
		for _, e := range escalations {
			sb.WriteString("- " + e.Title + "\n")
			if e.Detail != "" {
				sb.WriteString("  " + clip(e.Detail, 200) + "\n")
			}
		}
		sb.WriteByte('\n')
	}

	if len(criticals) > 0 {
		sb.WriteString("CRITICAL ISSUES:\n")
		for _, e := range criticals {
			sb.WriteString("- " + e.Title + "\n")
			if e.Detail != "" {
				sb.WriteString("  " + clip(e.Detail, 200) + "\n")
			}
		}
		sb.WriteByte('\n')
	}

	if len(approvals) > 0 {
		sb.WriteString("PENDING APPROVALS (agent waiting for operator decision):\n")
		for _, e := range approvals {
			sb.WriteString("- [" + e.Risk + " risk] " + e.Title + "\n")
			if e.ProposedAction != nil && *e.ProposedAction != "" {
				sb.WriteString("  Proposed action: " + clip(*e.ProposedAction, 150) + "\n")
			}
			if e.Detail != "" {
				sb.WriteString("  " + clip(e.Detail, 150) + "\n")
			}
		}
	}

	return sb.String()
}

func filterEntries(entries []TimelineEntry, fn func(TimelineEntry) bool) []TimelineEntry {
	var out []TimelineEntry
	for _, e := range entries {
		if fn(e) {
			out = append(out, e)
		}
	}
	return out
}

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
