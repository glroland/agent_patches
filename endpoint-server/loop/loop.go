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
	"agent_patches/endpoint-server/utils/config"
	"agent_patches/endpoint-server/utils/notifier"
)

const (
	defaultHeartbeat    = time.Second
	maxSummaryLen       = 300
	startupJitterMaxMin = 30 // upper bound (inclusive) for the random startup delay in minutes

	// AttrRunPrefix is the attrs key prefix for responsibility run state.
	// Full key: AttrRunPrefix + responsibility name.
	AttrRunPrefix = "responsibility_run:"
)

// RunState is the persisted outcome of a single responsibility execution.
type RunState struct {
	LastRunAt string `json:"lastRunAt"`
	Status    string `json:"status"` // "ok" or "error"
	Summary   string `json:"summary,omitempty"`
}

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
	slog.Info("loop: starting — frequency-based responsibilities are delayed by a random startup jitter to spread LLM load across the fleet on simultaneous restarts",
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

	a := agent.NewWithResponsibility(l.filterTools(r.cfg.Tools), l.cfg, l.cfg.ResponsibilitySystemPrompt, r.cfg.Name)
	result, err := a.Run(ctx, l.withOpenIncidents(r.cfg.Instruction))
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
	if writeErr := l.mem.Attrs().Set(AttrRunPrefix+r.cfg.Name, state); writeErr != nil {
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
