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
	"agent_patches/endpoint-server/utils/config"
	"agent_patches/endpoint-server/utils/notifier"
)

const defaultHeartbeat = time.Second

// Loop wakes up on a configurable interval, runs a tick handler, and
// dispatches any configured responsibilities that are due.
type Loop struct {
	cfg              *config.Settings
	registry         *tasks.Registry
	notify           *notifier.Notifier
	responsibilities []*responsibility
}

// New creates a Loop. Call Start to launch the background goroutine.
func New(cfg *config.Settings, registry *tasks.Registry, notify *notifier.Notifier) *Loop {
	resp := make([]*responsibility, 0, len(cfg.Responsibilities))
	for _, rc := range cfg.Responsibilities {
		r, err := newResponsibility(rc)
		if err != nil {
			slog.Error("loop: invalid responsibility, skipping", "name", rc.Name, "error", err)
			continue
		}
		resp = append(resp, r)
	}
	return &Loop{cfg: cfg, registry: registry, notify: notify, responsibilities: resp}
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
	slog.Debug("loop: tick")

	now := time.Now()
	for _, r := range l.responsibilities {
		if !r.due(now) {
			continue
		}
		r.schedule(now)

		if !r.running.CompareAndSwap(false, true) {
			slog.Error("loop: responsibility still in flight, skipping this run", "name", r.cfg.Name)
			continue
		}
		go l.execute(ctx, r)
	}
}

// execute runs a single responsibility's instruction through the agent and
// notifies the manager if configured to do so.
func (l *Loop) execute(ctx context.Context, r *responsibility) {
	defer r.running.Store(false)

	log := slog.With("responsibility", r.cfg.Name)
	log.Info("loop: responsibility started")

	a := agent.New(l.filterTools(r.cfg.Tools), l.cfg)
	result, err := a.Run(ctx, r.cfg.Instruction)
	if err != nil {
		log.Error("loop: responsibility failed", "error", err)
	} else {
		log.Info("loop: responsibility completed", "output_len", len(result))
		log.Debug("loop: responsibility output", "output", result)
	}

	l.maybeNotify(ctx, r, result, err)
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
func (l *Loop) maybeNotify(ctx context.Context, r *responsibility, result string, err error) {
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
