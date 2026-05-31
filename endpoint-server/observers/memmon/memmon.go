package memmon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"agent_patches/endpoint-server/utils/config"
	"agent_patches/endpoint-server/utils/notifier"
)

// MemStat holds memory usage statistics for the current host.
type MemStat struct {
	Total     uint64 // total physical RAM in bytes
	Available uint64 // available RAM (free + reclaimable) in bytes
	SwapTotal uint64 // total swap / page-file space in bytes
	SwapFree  uint64 // free swap / page-file space in bytes
}

func (m MemStat) Used() uint64 {
	if m.Available > m.Total {
		return 0
	}
	return m.Total - m.Available
}

func (m MemStat) UsedPct() float64 {
	if m.Total == 0 {
		return 0
	}
	return float64(m.Used()) / float64(m.Total) * 100
}

func (m MemStat) SwapUsed() uint64 {
	if m.SwapFree > m.SwapTotal {
		return 0
	}
	return m.SwapTotal - m.SwapFree
}

func (m MemStat) SwapUsedPct() float64 {
	if m.SwapTotal == 0 {
		return 0
	}
	return float64(m.SwapUsed()) / float64(m.SwapTotal) * 100
}

// Monitor periodically checks system memory and notifies when RAM or swap
// usage exceeds the configured thresholds.
type Monitor struct {
	cfg       *config.MemoryMonitorSettings
	notifier  *notifier.Notifier
	GetMemory func() (MemStat, error) // overridable for tests
}

// New creates a Monitor.
func New(cfg *config.MemoryMonitorSettings, n *notifier.Notifier) *Monitor {
	return &Monitor{
		cfg:       cfg,
		notifier:  n,
		GetMemory: localMemory,
	}
}

// Start launches the background polling loop in a new goroutine and returns
// immediately. The goroutine exits when ctx is cancelled.
func (m *Monitor) Start(ctx context.Context) {
	if !m.cfg.Enabled {
		slog.Info("memory_monitor: disabled")
		return
	}
	interval, err := time.ParseDuration(m.cfg.Interval)
	if err != nil || interval <= 0 {
		slog.Error("memory_monitor: invalid interval, defaulting to 5m",
			"interval", m.cfg.Interval, "error", err)
		interval = 5 * time.Minute
	}
	slog.Info("memory_monitor: starting",
		"ram_threshold_pct", m.cfg.ThresholdPercent,
		"swap_threshold_pct", m.cfg.SwapThresholdPercent,
		"interval", interval,
	)
	go m.loop(ctx, interval)
}

func (m *Monitor) loop(ctx context.Context, interval time.Duration) {
	m.Check(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("memory_monitor: stopped")
			return
		case <-ticker.C:
			m.Check(ctx)
		}
	}
}

// Check samples system memory and notifies if any threshold is exceeded.
// Exported so tests can drive it directly without running the goroutine.
func (m *Monitor) Check(ctx context.Context) {
	stat, err := m.GetMemory()
	if err != nil {
		slog.Warn("memory_monitor: failed to read memory stats", "error", err)
		return
	}

	slog.Debug("memory_monitor: sampled",
		"ram_used_pct", fmt.Sprintf("%.1f%%", stat.UsedPct()),
		"ram_total", formatBytes(stat.Total),
		"swap_used_pct", fmt.Sprintf("%.1f%%", stat.SwapUsedPct()),
	)

	ramAlert := stat.UsedPct() >= m.cfg.ThresholdPercent
	swapAlert := m.cfg.SwapThresholdPercent > 0 &&
		stat.SwapTotal > 0 &&
		stat.SwapUsedPct() >= m.cfg.SwapThresholdPercent

	if !ramAlert && !swapAlert {
		slog.Debug("memory_monitor: within thresholds")
		return
	}

	var label string
	switch {
	case ramAlert && swapAlert:
		label = "RAM + Swap"
	case ramAlert:
		label = "RAM"
	default:
		label = "Swap"
	}

	host, _ := os.Hostname()
	slog.Warn("memory_monitor: threshold exceeded",
		"host", host, "alert", label,
		"ram_used_pct", fmt.Sprintf("%.1f%%", stat.UsedPct()),
		"swap_used_pct", fmt.Sprintf("%.1f%%", stat.SwapUsedPct()),
	)
	m.notifier.Notify(ctx,
		fmt.Sprintf("[%s] Memory Alert (%s)", host, label),
		BuildMessage(host, m.cfg.ThresholdPercent, m.cfg.SwapThresholdPercent, stat),
	)
}

// BuildMessage composes the notification body for a memory alert.
func BuildMessage(host string, ramThresholdPct, swapThresholdPct float64, stat MemStat) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Memory usage threshold exceeded on host %q.\n\n", host)
	fmt.Fprintf(&sb, "RAM\n")
	fmt.Fprintf(&sb, "  Total:     %s\n", formatBytes(stat.Total))
	fmt.Fprintf(&sb, "  Used:      %s (%.1f%%)\n", formatBytes(stat.Used()), stat.UsedPct())
	fmt.Fprintf(&sb, "  Available: %s\n", formatBytes(stat.Available))
	fmt.Fprintf(&sb, "  Threshold: %.0f%%\n", ramThresholdPct)
	if stat.SwapTotal > 0 {
		fmt.Fprintf(&sb, "\nSwap\n")
		fmt.Fprintf(&sb, "  Total:     %s\n", formatBytes(stat.SwapTotal))
		fmt.Fprintf(&sb, "  Used:      %s (%.1f%%)\n", formatBytes(stat.SwapUsed()), stat.SwapUsedPct())
		fmt.Fprintf(&sb, "  Free:      %s\n", formatBytes(stat.SwapFree))
		if swapThresholdPct > 0 {
			fmt.Fprintf(&sb, "  Threshold: %.0f%%\n", swapThresholdPct)
		}
	}
	return sb.String()
}

func formatBytes(b uint64) string {
	switch {
	case b >= 1 << 40:
		return fmt.Sprintf("%.2f TB", float64(b)/(1<<40))
	case b >= 1 << 30:
		return fmt.Sprintf("%.2f GB", float64(b)/(1<<30))
	case b >= 1 << 20:
		return fmt.Sprintf("%.2f MB", float64(b)/(1<<20))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
