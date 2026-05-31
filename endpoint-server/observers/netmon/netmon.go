package netmon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"agent_patches/endpoint-server/utils/config"
	"agent_patches/endpoint-server/utils/notifier"
)

// UploadMonitor periodically samples network counters and notifies when the
// outbound (upload) rate exceeds the configured threshold.
type UploadMonitor struct {
	cfg      *config.NetworkUploadSettings
	notifier *notifier.Notifier
	// Snapshot returns cumulative (bytesIn, bytesOut) across all non-loopback
	// interfaces. Overridable for tests.
	Snapshot func() (in, out uint64, err error)

	mu       sync.Mutex
	lastOut  uint64
	lastTime time.Time
}

// DownloadMonitor periodically samples network counters and notifies when the
// inbound (download) rate exceeds the configured threshold.
type DownloadMonitor struct {
	cfg      *config.NetworkDownloadSettings
	notifier *notifier.Notifier
	// Snapshot returns cumulative (bytesIn, bytesOut) across all non-loopback
	// interfaces. Overridable for tests.
	Snapshot func() (in, out uint64, err error)

	mu       sync.Mutex
	lastIn   uint64
	lastTime time.Time
}

// NewUploadMonitor creates an UploadMonitor.
func NewUploadMonitor(cfg *config.NetworkUploadSettings, n *notifier.Notifier) *UploadMonitor {
	return &UploadMonitor{cfg: cfg, notifier: n, Snapshot: snapshot}
}

// NewDownloadMonitor creates a DownloadMonitor.
func NewDownloadMonitor(cfg *config.NetworkDownloadSettings, n *notifier.Notifier) *DownloadMonitor {
	return &DownloadMonitor{cfg: cfg, notifier: n, Snapshot: snapshot}
}

// Start launches the background polling loop and returns immediately.
func (m *UploadMonitor) Start(ctx context.Context) {
	if !m.cfg.Enabled {
		slog.Info("net_upload_monitor: disabled")
		return
	}
	interval, err := time.ParseDuration(m.cfg.Interval)
	if err != nil || interval <= 0 {
		slog.Error("net_upload_monitor: invalid interval, defaulting to 1m",
			"interval", m.cfg.Interval, "error", err)
		interval = time.Minute
	}
	slog.Info("net_upload_monitor: starting",
		"threshold_mbps", m.cfg.ThresholdMBps, "interval", interval)
	go m.loop(ctx, interval)
}

func (m *UploadMonitor) loop(ctx context.Context, interval time.Duration) {
	m.Check(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("net_upload_monitor: stopped")
			return
		case <-ticker.C:
			m.Check(ctx)
		}
	}
}

// Check samples the current upload rate and notifies if the threshold is exceeded.
// Exported so tests can drive it directly.
func (m *UploadMonitor) Check(ctx context.Context) {
	_, out, err := m.Snapshot()
	if err != nil {
		slog.Warn("net_upload_monitor: snapshot failed", "error", err)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	if m.lastTime.IsZero() {
		m.lastOut = out
		m.lastTime = now
		return
	}

	elapsed := now.Sub(m.lastTime).Seconds()
	if elapsed < 0.5 {
		return // too soon for a meaningful delta
	}

	var rateMBps float64
	if out >= m.lastOut {
		rateMBps = float64(out-m.lastOut) / elapsed / (1024 * 1024)
	}
	m.lastOut = out
	m.lastTime = now

	slog.Debug("net_upload_monitor: sampled", "rate_mbps", fmt.Sprintf("%.2f", rateMBps))

	if rateMBps < m.cfg.ThresholdMBps {
		return
	}
	host, _ := os.Hostname()
	slog.Warn("net_upload_monitor: threshold exceeded",
		"host", host, "rate_mbps", fmt.Sprintf("%.2f", rateMBps),
		"threshold_mbps", m.cfg.ThresholdMBps)
	m.notifier.Notify(ctx,
		fmt.Sprintf("[%s] Network Upload Alert", host),
		BuildUploadMessage(host, m.cfg.ThresholdMBps, rateMBps),
	)
}

// Start launches the background polling loop and returns immediately.
func (m *DownloadMonitor) Start(ctx context.Context) {
	if !m.cfg.Enabled {
		slog.Info("net_download_monitor: disabled")
		return
	}
	interval, err := time.ParseDuration(m.cfg.Interval)
	if err != nil || interval <= 0 {
		slog.Error("net_download_monitor: invalid interval, defaulting to 1m",
			"interval", m.cfg.Interval, "error", err)
		interval = time.Minute
	}
	slog.Info("net_download_monitor: starting",
		"threshold_mbps", m.cfg.ThresholdMBps, "interval", interval)
	go m.loop(ctx, interval)
}

func (m *DownloadMonitor) loop(ctx context.Context, interval time.Duration) {
	m.Check(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("net_download_monitor: stopped")
			return
		case <-ticker.C:
			m.Check(ctx)
		}
	}
}

// Check samples the current download rate and notifies if the threshold is exceeded.
// Exported so tests can drive it directly.
func (m *DownloadMonitor) Check(ctx context.Context) {
	in, _, err := m.Snapshot()
	if err != nil {
		slog.Warn("net_download_monitor: snapshot failed", "error", err)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	if m.lastTime.IsZero() {
		m.lastIn = in
		m.lastTime = now
		return
	}

	elapsed := now.Sub(m.lastTime).Seconds()
	if elapsed < 0.5 {
		return
	}

	var rateMBps float64
	if in >= m.lastIn {
		rateMBps = float64(in-m.lastIn) / elapsed / (1024 * 1024)
	}
	m.lastIn = in
	m.lastTime = now

	slog.Debug("net_download_monitor: sampled", "rate_mbps", fmt.Sprintf("%.2f", rateMBps))

	if rateMBps < m.cfg.ThresholdMBps {
		return
	}
	host, _ := os.Hostname()
	slog.Warn("net_download_monitor: threshold exceeded",
		"host", host, "rate_mbps", fmt.Sprintf("%.2f", rateMBps),
		"threshold_mbps", m.cfg.ThresholdMBps)
	m.notifier.Notify(ctx,
		fmt.Sprintf("[%s] Network Download Alert", host),
		BuildDownloadMessage(host, m.cfg.ThresholdMBps, rateMBps),
	)
}

// BuildUploadMessage composes the notification body for an upload rate alert.
func BuildUploadMessage(host string, thresholdMBps, rateMBps float64) string {
	return fmt.Sprintf(
		"Network upload rate threshold exceeded on host %q.\n\nRate:      %s\nThreshold: %s\n",
		host, formatBandwidth(rateMBps), formatBandwidth(thresholdMBps),
	)
}

// BuildDownloadMessage composes the notification body for a download rate alert.
func BuildDownloadMessage(host string, thresholdMBps, rateMBps float64) string {
	return fmt.Sprintf(
		"Network download rate threshold exceeded on host %q.\n\nRate:      %s\nThreshold: %s\n",
		host, formatBandwidth(rateMBps), formatBandwidth(thresholdMBps),
	)
}

func formatBandwidth(mbps float64) string {
	switch {
	case mbps >= 1024:
		return fmt.Sprintf("%.2f GB/s", mbps/1024)
	case mbps >= 1:
		return fmt.Sprintf("%.2f MB/s", mbps)
	default:
		return fmt.Sprintf("%.2f KB/s", mbps*1024)
	}
}
