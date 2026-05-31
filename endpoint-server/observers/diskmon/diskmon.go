package diskmon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/utils/config"
	"agent_patches/endpoint-server/utils/notifier"
)

type diskMemSnap struct {
	Mount      string  `json:"mount"`
	FSType     string  `json:"fs_type,omitempty"`
	TotalBytes uint64  `json:"total_bytes"`
	FreeBytes  uint64  `json:"free_bytes"`
	UsedBytes  uint64  `json:"used_bytes"`
	UsedPct    float64 `json:"used_pct"`
}

type diskSnapshot struct {
	CheckedAt time.Time     `json:"checked_at"`
	Disks     []diskMemSnap `json:"disks"`
}

// DiskStat holds usage statistics for one local disk.
type DiskStat struct {
	Mount  string
	Total  uint64
	Free   uint64
	FSType string
}

func (d DiskStat) Used() uint64 {
	if d.Free > d.Total {
		return 0
	}
	return d.Total - d.Free
}

func (d DiskStat) UsedPct() float64 {
	if d.Total == 0 {
		return 0
	}
	return float64(d.Used()) / float64(d.Total) * 100
}

// Monitor periodically checks all local disks and notifies when any exceeds
// the configured usage threshold.
type Monitor struct {
	cfg      *config.DiskMonitorSettings
	notifier *notifier.Notifier
	GetDisks func() ([]DiskStat, error)    // overridable for tests
	Mem      *memory.DomainStore           // optional; nil disables memory writes
}

// New creates a Monitor.
func New(cfg *config.DiskMonitorSettings, n *notifier.Notifier) *Monitor {
	return &Monitor{
		cfg:      cfg,
		notifier: n,
		GetDisks: localDisks,
	}
}

// Start launches the background polling loop in a new goroutine and returns
// immediately. The goroutine exits when ctx is cancelled.
func (m *Monitor) Start(ctx context.Context) {
	if !m.cfg.Enabled {
		slog.Info("disk_monitor: disabled")
		return
	}
	interval, err := time.ParseDuration(m.cfg.Interval)
	if err != nil || interval <= 0 {
		slog.Error("disk_monitor: invalid interval, defaulting to 1h",
			"interval", m.cfg.Interval, "error", err)
		interval = time.Hour
	}
	slog.Info("disk_monitor: starting",
		"threshold_pct", m.cfg.ThresholdPercent,
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
			slog.Info("disk_monitor: stopped")
			return
		case <-ticker.C:
			m.Check(ctx)
		}
	}
}

// Check samples all local disks and notifies for any that exceed the threshold.
// Exported so tests can drive it directly without running the goroutine.
func (m *Monitor) Check(ctx context.Context) {
	disks, err := m.GetDisks()
	if err != nil {
		slog.Warn("disk_monitor: failed to enumerate disks", "error", err)
		return
	}

	var alerts []DiskStat
	for _, d := range disks {
		slog.Debug("disk_monitor: sampled",
			"mount", d.Mount,
			"used_pct", fmt.Sprintf("%.1f%%", d.UsedPct()),
			"total", formatBytes(d.Total),
		)
		if d.UsedPct() >= m.cfg.ThresholdPercent {
			alerts = append(alerts, d)
		}
	}

	if m.Mem != nil {
		snaps := make([]diskMemSnap, 0, len(disks))
		for _, d := range disks {
			snaps = append(snaps, diskMemSnap{
				Mount:      d.Mount,
				FSType:     d.FSType,
				TotalBytes: d.Total,
				FreeBytes:  d.Free,
				UsedBytes:  d.Used(),
				UsedPct:    d.UsedPct(),
			})
		}
		if err := m.Mem.Write(diskSnapshot{CheckedAt: time.Now().UTC(), Disks: snaps}); err != nil {
			slog.Debug("disk_monitor: memory write failed", "error", err)
		}
	}

	if len(alerts) == 0 {
		slog.Debug("disk_monitor: all disks within threshold")
		return
	}

	host, _ := os.Hostname()
	slog.Warn("disk_monitor: threshold exceeded",
		"host", host,
		"disks_over_threshold", len(alerts),
		"threshold_pct", m.cfg.ThresholdPercent,
	)
	m.notifier.Notify(ctx,
		fmt.Sprintf("[%s] Disk Space Alert", host),
		BuildMessage(host, m.cfg.ThresholdPercent, alerts),
	)
}

// BuildMessage composes the notification body for a disk space alert.
func BuildMessage(host string, thresholdPct float64, alerts []DiskStat) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Disk usage threshold (%.0f%%) exceeded on host %q.\n\n", thresholdPct, host)
	for i, d := range alerts {
		fmt.Fprintf(&sb, "Mount:      %s\n", d.Mount)
		if d.FSType != "" {
			fmt.Fprintf(&sb, "Filesystem: %s\n", d.FSType)
		}
		fmt.Fprintf(&sb, "Total:      %s\n", formatBytes(d.Total))
		fmt.Fprintf(&sb, "Used:       %s (%.1f%%)\n", formatBytes(d.Used()), d.UsedPct())
		fmt.Fprintf(&sb, "Free:       %s\n", formatBytes(d.Free))
		if i < len(alerts)-1 {
			fmt.Fprintf(&sb, "\n")
		}
	}
	return sb.String()
}

func formatBytes(b uint64) string {
	switch {
	case b >= 1<<40:
		return fmt.Sprintf("%.2f TB", float64(b)/(1<<40))
	case b >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.2f MB", float64(b)/(1<<20))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
