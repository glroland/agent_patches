// Package loop provides a generic background loop that wakes up on a
// configurable interval for the lifetime of the application.
package loop

import (
	"context"
	"log/slog"
	"time"

	"agent_patches/endpoint-server/utils/config"
)

const defaultInterval = 60 * time.Second

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
	interval, err := time.ParseDuration(l.cfg.Interval)
	if err != nil || interval <= 0 {
		slog.Error("loop: invalid interval, defaulting", "interval", l.cfg.Interval, "default", defaultInterval, "error", err)
		interval = defaultInterval
	}
	slog.Info("loop: starting", "interval", interval)
	go l.run(ctx, interval)
}

func (l *Loop) run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
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
