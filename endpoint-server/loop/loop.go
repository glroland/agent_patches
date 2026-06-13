// Package loop provides a generic background loop that wakes up on a
// configurable interval for the lifetime of the application.
package loop

import (
	"context"
	"log/slog"
	"time"

	"agent_patches/endpoint-server/utils/config"
)

const defaultHeartbeat = time.Second

// Loop wakes up on a configurable interval and runs a tick handler.
type Loop struct {
	cfg *config.LoopSettings
}

// New creates a Loop. Call Start to launch the background goroutine.
func New(cfg *config.LoopSettings) *Loop {
	return &Loop{cfg: cfg}
}

// Start launches the background loop. It returns immediately; the goroutine
// exits when ctx is cancelled.
func (l *Loop) Start(ctx context.Context) {
	heartbeat, err := time.ParseDuration(l.cfg.Heartbeat)
	if err != nil || heartbeat <= 0 {
		slog.Error("loop: invalid heartbeat, defaulting", "heartbeat", l.cfg.Heartbeat, "default", defaultHeartbeat, "error", err)
		heartbeat = defaultHeartbeat
	}
	slog.Info("loop: starting", "heartbeat", heartbeat)
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
}
