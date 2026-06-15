package analyze_memory_utilization

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skillstate"
)

// RAM usage thresholds (percent used) for the skill's last-known-state health.
const (
	usedPctWarning  = 80.0
	usedPctCritical = 90.0
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

type memoryUsageInput struct{}

// NewMemoryUsageTool returns a task tool that reports current RAM and swap
// usage for the host. The result is also recorded as the skill's last known
// state (see skillstate), so high memory pressure is reflected in
// GET /status even if the agent never calls report_findings.
func NewMemoryUsageTool(mem *memory.Store) (tool.Tool, error) {
	return tool.New(
		"analyze_memory_utilization",
		"Reports current RAM and swap usage for the host, including total, "+
			"used, and available memory.",
		func(_ context.Context, _ memoryUsageInput) (string, error) {
			slog.Info("analyze_memory_utilization: starting")
			stat, err := localMemory()
			if err != nil {
				slog.Info("analyze_memory_utilization: failed", "error", err)
				_ = skillstate.Save(mem, "analyze_memory_utilization", skillstate.HealthCritical, fmt.Sprintf("failed to read memory usage: %v", err))
				return "", fmt.Errorf("memory_usage: %w", err)
			}
			slog.Debug("analyze_memory_utilization: read stats",
				"total", stat.Total, "available", stat.Available,
				"swap_total", stat.SwapTotal, "swap_free", stat.SwapFree)
			report := BuildReport(stat)
			slog.Info("analyze_memory_utilization: completed",
				"used_pct", fmt.Sprintf("%.1f", stat.UsedPct()), "output_len", len(report))
			health, summary := memoryHealth(stat)
			_ = skillstate.Save(mem, "analyze_memory_utilization", health, summary)
			return report, nil
		},
	)
}

// memoryHealth derives a skillstate health/summary pair from a memory
// snapshot: critical if RAM usage is at/above usedPctCritical, warning if
// at/above usedPctWarning, else ok.
func memoryHealth(stat MemStat) (skillstate.Health, string) {
	pct := stat.UsedPct()
	summary := fmt.Sprintf("RAM used: %.1f%% (%s / %s)", pct, formatBytes(stat.Used()), formatBytes(stat.Total))
	switch {
	case pct >= usedPctCritical:
		return skillstate.HealthCritical, summary
	case pct >= usedPctWarning:
		return skillstate.HealthWarning, summary
	default:
		return skillstate.HealthOK, summary
	}
}

// BuildReport composes a human-readable summary of memory usage.
func BuildReport(stat MemStat) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "RAM\n")
	fmt.Fprintf(&sb, "  Total:     %s\n", formatBytes(stat.Total))
	fmt.Fprintf(&sb, "  Used:      %s (%.1f%%)\n", formatBytes(stat.Used()), stat.UsedPct())
	fmt.Fprintf(&sb, "  Available: %s\n", formatBytes(stat.Available))
	if stat.SwapTotal > 0 {
		fmt.Fprintf(&sb, "\nSwap\n")
		fmt.Fprintf(&sb, "  Total:     %s\n", formatBytes(stat.SwapTotal))
		fmt.Fprintf(&sb, "  Used:      %s (%.1f%%)\n", formatBytes(stat.SwapUsed()), stat.SwapUsedPct())
		fmt.Fprintf(&sb, "  Free:      %s\n", formatBytes(stat.SwapFree))
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
