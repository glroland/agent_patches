// Package loop provides a generic background loop that wakes up on a
// configurable interval for the lifetime of the application and dispatches
// configured responsibilities when they are due.
package loop

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"agent_patches/endpoint-server/a2a/agent"
	tasks "agent_patches/endpoint-server/a2a/registry"
	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/utils/config"
	"agent_patches/endpoint-server/utils/notifier"
)

const (
	defaultHeartbeat = time.Second
	maxSummaryLen    = 300

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
	responsibilities []*Responsibility
}

// New creates a Loop. Call Start to launch the background goroutine.
func New(cfg *config.Settings, registry *tasks.Registry, notify *notifier.Notifier, mem *memory.Store) *Loop {
	resp := make([]*Responsibility, 0, len(cfg.Responsibilities))
	for _, rc := range cfg.Responsibilities {
		r, err := NewResponsibility(rc)
		if err != nil {
			slog.Error("loop: invalid responsibility, skipping", "name", rc.Name, "error", err)
			continue
		}
		resp = append(resp, r)
	}
	return &Loop{cfg: cfg, registry: registry, notify: notify, mem: mem, responsibilities: resp}
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
	slog.Info("loop: starting", "heartbeat", heartbeat, "responsibilities", len(l.responsibilities))
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

	log := slog.With("responsibility", r.cfg.Name)
	log.Info("loop: responsibility started")

	a := agent.NewWithSystemPrompt(l.filterTools(r.cfg.Tools), l.cfg, l.cfg.ResponsibilitySystemPrompt)
	result, err := a.Run(ctx, r.cfg.Instruction)
	if err != nil {
		log.Error("loop: responsibility failed", "error", err)
	} else {
		log.Info("loop: responsibility completed", "output_len", len(result))
		log.Debug("loop: responsibility output", "output", result)
	}

	l.persistRunState(r, result, err)
	l.maybeNotify(ctx, r, result, err)
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
