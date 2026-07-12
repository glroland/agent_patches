// Package loop provides a generic background loop that wakes up on a
// configurable interval for the lifetime of the application and dispatches
// configured responsibilities when they are due.
package loop

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"agent_patches/endpoint-server/a2a/agent"
	tasks "agent_patches/endpoint-server/a2a/registry"
	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/incidents"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skillstate"
	"agent_patches/endpoint-server/utils/config"
	"agent_patches/endpoint-server/utils/notifier"
)

const (
	defaultHeartbeat    = time.Second
	maxSummaryLen       = 300
	startupJitterMaxMin = 30 // upper bound (inclusive) for the random startup delay in minutes

	// AttrRunPrefix is the key prefix for responsibility run state within
	// skillstate.Domain. Full key: AttrRunPrefix + responsibility name.
	AttrRunPrefix = "responsibility_run:"
)

// RunState is the persisted outcome of a single responsibility execution.
type RunState struct {
	LastRunAt string `json:"lastRunAt"`
	Status    string `json:"status"` // "ok" or "error"
	Summary   string `json:"summary,omitempty"`
}

// PreCheck runs deterministic, non-LLM logic before a responsibility's agent
// invocation. If needsLLM is false, the loop skips the LLM call entirely and
// treats report as the run's summary — this is the mechanism responsibilities
// use to avoid paying for an inference call on every tick when there's
// nothing worth escalating. When needsLLM is true, report (if non-empty) is
// appended to the responsibility's instruction so the agent doesn't have to
// re-derive data the pre-check already gathered. On error, callers should
// fail open (return needsLLM=true) so a broken pre-check surfaces via the LLM
// path instead of silently suppressing real incidents.
type PreCheck func(ctx context.Context) (needsLLM bool, report string, err error)

// Loop wakes up on a configurable interval, runs a tick handler, and
// dispatches any configured responsibilities that are due.
type Loop struct {
	cfg              *config.Settings
	registry         *tasks.Registry
	notify           *notifier.Notifier
	mem              *memory.Store
	incidents        *incidents.Store
	responsibilities []*Responsibility
	startupDelay     time.Duration
	preChecks        map[string]PreCheck
}

// New creates a Loop. Call Start to launch the background goroutine.
func New(cfg *config.Settings, registry *tasks.Registry, notify *notifier.Notifier, mem *memory.Store, inc *incidents.Store) *Loop {
	jitterMin := rand.Intn(startupJitterMaxMin + 1) // 0–30 inclusive
	startupDelay := time.Duration(jitterMin) * time.Minute

	resp := make([]*Responsibility, 0, len(cfg.Responsibilities))
	for _, rc := range cfg.Responsibilities {
		r, err := NewResponsibility(rc, startupDelay)
		if err != nil {
			slog.Error("loop: invalid responsibility, skipping", "name", rc.Name, "error", err)
			continue
		}
		resp = append(resp, r)
	}
	return &Loop{cfg: cfg, registry: registry, notify: notify, mem: mem, incidents: inc, responsibilities: resp, startupDelay: startupDelay}
}

// Responsibilities returns the live list of scheduled responsibilities.
func (l *Loop) Responsibilities() []*Responsibility { return l.responsibilities }

// RegisterPreCheck attaches a PreCheck to the named responsibility. It must
// be called before the loop starts ticking (i.e., before Start).
func (l *Loop) RegisterPreCheck(name string, pc PreCheck) {
	if l.preChecks == nil {
		l.preChecks = make(map[string]PreCheck)
	}
	l.preChecks[name] = pc
}

// CurrentTask returns the name of the responsibility currently in flight, or
// "" if none is running. If multiple are running concurrently, the first one
// found is returned. Deprecated: prefer RunningTasks.
func (l *Loop) CurrentTask() string {
	tasks := l.RunningTasks()
	if len(tasks) == 0 {
		return ""
	}
	return tasks[0]
}

// RunningTasks returns the names of all responsibilities currently in flight.
// Returns nil when none are running.
func (l *Loop) RunningTasks() []string {
	var running []string
	for _, r := range l.responsibilities {
		if r.Running.Load() {
			running = append(running, r.Name())
		}
	}
	return running
}

// Start launches the background loop. It returns immediately; the goroutine
// exits when ctx is cancelled.
func (l *Loop) Start(ctx context.Context) {
	heartbeat, err := time.ParseDuration(l.cfg.Loop.Heartbeat)
	if err != nil || heartbeat <= 0 {
		slog.Error("loop: invalid heartbeat, defaulting", "heartbeat", l.cfg.Loop.Heartbeat, "default", defaultHeartbeat, "error", err)
		heartbeat = defaultHeartbeat
	}
	slog.Info("loop: starting — responsibilities are offset by a random startup jitter (frequency-based: first run delayed; time-of-day: daily time shifted) to spread LLM load across the fleet",
		"heartbeat", heartbeat,
		"responsibilities", len(l.responsibilities),
		"startup_jitter_minutes", int(l.startupDelay.Minutes()),
	)
	go l.run(ctx, heartbeat)
}

func (l *Loop) run(ctx context.Context, heartbeat time.Duration) {
	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("loop: stopped")
			return
		case <-ticker.C:
			l.tick(ctx)
		}
	}
}

// tick runs on every wake-up.
func (l *Loop) tick(ctx context.Context) {
	// slog.Debug("loop: tick")

	now := time.Now()
	for _, r := range l.responsibilities {
		if !r.Due(now) {
			continue
		}
		r.Schedule(now)

		if !r.Running.CompareAndSwap(false, true) {
			slog.Error("loop: responsibility still in flight, skipping this run", "name", r.cfg.Name)
			continue
		}
		go l.execute(ctx, r)
	}
}

// execute runs a single responsibility's instruction through the agent and
// notifies the manager if configured to do so.
func (l *Loop) execute(ctx context.Context, r *Responsibility) {
	defer r.Running.Store(false)

	ctx, span := otel.Tracer("agent_patches/loop").Start(ctx, "responsibility.run",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attribute.String("responsibility.name", r.cfg.Name)),
	)
	defer span.End()

	log := slog.With("responsibility", r.cfg.Name)
	log.Info("loop: responsibility started")

	instruction := r.cfg.Instruction
	if pc, ok := l.preChecks[r.cfg.Name]; ok {
		needsLLM, report, pcErr := pc(ctx)
		if pcErr != nil {
			log.Warn("loop: pre-check failed, falling back to LLM", "error", pcErr)
		} else if !needsLLM {
			log.Info("loop: pre-check clean, skipping LLM call", "summary", report)
			span.SetStatus(codes.Ok, "")
			l.persistRunState(r, report, nil)
			l.maybeNotify(ctx, r, report, nil)
			return
		} else if report != "" {
			instruction = instruction + "\n\nPre-gathered health report:\n" + report
		}
	}

	a := agent.NewWithResponsibility(l.filterTools(r.cfg.Tools), l.cfg, l.cfg.ResponsibilitySystemPrompt, r.cfg.Name)
	result, err := a.Run(ctx, l.withOpenIncidents(instruction))
	if err != nil {
		log.Error("loop: responsibility failed", "error", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		log.Info("loop: responsibility completed", "output_len", len(result))
		log.Debug("loop: responsibility output", "output", result)
		span.SetStatus(codes.Ok, "")
	}

	l.persistRunState(r, result, err)
	l.maybeNotify(ctx, r, result, err)
}

// withOpenIncidents appends the open-incident ledger to a responsibility
// instruction so every run starts knowing what is already being tracked,
// instead of rediscovering (and re-reporting) the same problems each cycle.
func (l *Loop) withOpenIncidents(instruction string) string {
	summary := l.incidents.OpenSummary()
	if summary == "" {
		return instruction
	}
	return instruction + "\n\n" +
		"OPEN INCIDENTS already being tracked from previous runs:\n" + summary + "\n" +
		"Do not file duplicate findings for these. If you observe one of them again, " +
		"record the recurrence (manage_incidents action=report with the same fingerprint) " +
		"or log any action you take against it (action=log_action). If it is no longer " +
		"occurring, resolve it (action=resolve) and mention that in your report. " +
		"Open a new incident only for a problem not covered by the list above."
}

// persistRunState writes the outcome of a responsibility run to attrs so it
// survives restarts and is readable by the responsibilities API.
func (l *Loop) persistRunState(r *Responsibility, result string, err error) {
	status := "ok"
	if err != nil {
		status = "error"
	}
	summary := result
	if len(summary) > maxSummaryLen {
		summary = summary[:maxSummaryLen] + "..."
	}
	if err != nil && summary == "" {
		summary = err.Error()
		if len(summary) > maxSummaryLen {
			summary = summary[:maxSummaryLen] + "..."
		}
	}
	state := RunState{
		LastRunAt: time.Now().UTC().Format(time.RFC3339),
		Status:    status,
		Summary:   summary,
	}
	if writeErr := l.mem.Domain(skillstate.Domain).SetKey(AttrRunPrefix+r.cfg.Name, state); writeErr != nil {
		slog.Warn("loop: failed to persist run state", "responsibility", r.cfg.Name, "error", writeErr)
	}
}

// filterTools returns the subset of the registry's tools named in names, in
// the order given. Unknown names are logged and skipped.
func (l *Loop) filterTools(names []string) []tool.Tool {
	if len(names) == 0 {
		return nil
	}

	index := make(map[string]tool.Tool, len(l.registry.Tools()))
	for _, t := range l.registry.Tools() {
		index[t.Name()] = t
	}

	out := make([]tool.Tool, 0, len(names))
	for _, name := range names {
		t, ok := index[name]
		if !ok {
			slog.Warn("loop: responsibility references unknown tool", "tool", name)
			continue
		}
		out = append(out, t)
	}
	return out
}

// maybeNotify sends a notification for a responsibility's outcome based on
// its WhenToNotify setting: "always" notifies on every run, "on_error" (or
// "on error", the default) notifies only when the run failed, and "never"
// suppresses notifications entirely.
func (l *Loop) maybeNotify(ctx context.Context, r *Responsibility, result string, err error) {
	mode := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(r.cfg.WhenToNotify), " ", "_"))

	if err != nil {
		if mode == "never" {
			return
		}
		l.notify.Notify(ctx, fmt.Sprintf("[%s] responsibility failed", r.cfg.Name), fmt.Sprintf("Error: %v", err))
		return
	}

	if mode == "always" {
		l.notify.Notify(ctx, fmt.Sprintf("[%s] responsibility report", r.cfg.Name), result)
	}
}
