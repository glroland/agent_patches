package analyze_cpu_utilization

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skillstate"
)

// CPU usage thresholds (percent used) for the skill's last-known-state health.
const (
	usedPctWarning  = 80.0
	usedPctCritical = 90.0
)

// CPUStat holds a point-in-time CPU utilization snapshot.
type CPUStat struct {
	UsedPct   float64 // overall CPU usage percentage (0–100), sampled over ~1 second
	NumCPU    int     // number of logical CPUs
	LoadAvg1  float64 // 1-minute load average
	LoadAvg5  float64 // 5-minute load average
	LoadAvg15 float64 // 15-minute load average
}

type cpuUsageInput struct{}

// runCheck samples CPU utilization, records the health as a skillstate entry,
// and returns a human-readable report. It makes no LLM calls — callers decide
// whether the result warrants one.
func runCheck(ctx context.Context, mem *memory.Store) (stat CPUStat, report string, err error) {
	slog.Info("analyze_cpu_utilization: starting")
	stat, err = localCPU(ctx)
	if err != nil {
		slog.Info("analyze_cpu_utilization: failed", "error", err)
		_ = skillstate.Save(mem, "analyze_cpu_utilization", skillstate.HealthCritical,
			fmt.Sprintf("failed to read CPU usage: %v", err))
		return CPUStat{}, "", fmt.Errorf("cpu_usage: %w", err)
	}
	slog.Debug("analyze_cpu_utilization: read stats",
		"used_pct", stat.UsedPct, "num_cpu", stat.NumCPU,
		"load1", stat.LoadAvg1, "load5", stat.LoadAvg5, "load15", stat.LoadAvg15)
	report = BuildReport(stat)
	slog.Info("analyze_cpu_utilization: completed",
		"used_pct", fmt.Sprintf("%.1f", stat.UsedPct), "output_len", len(report))
	health, summary := cpuHealth(stat)
	_ = skillstate.Save(mem, "analyze_cpu_utilization", health, summary)
	return stat, report, nil
}

// NewCPUUsageTool returns a task tool that reports current CPU utilization for
// the host. The result is also recorded as the skill's last known state (see
// skillstate), so sustained high CPU usage is reflected in GET /status even if
// the agent never calls report_findings.
func NewCPUUsageTool(mem *memory.Store) (tool.Tool, error) {
	return tool.New(
		"analyze_cpu_utilization",
		"Reports current CPU utilization for the host, including overall usage "+
			"percentage (sampled over ~1 second), logical CPU count, and load averages.",
		func(ctx context.Context, _ cpuUsageInput) (string, error) {
			_, report, err := runCheck(ctx, mem)
			return report, err
		},
	)
}

// NeedsInvestigation reports whether a CPU snapshot crosses the
// cpu-utilization-check responsibility's own escalation bar: usage at/above
// usedPctWarning, or any load average exceeding the logical CPU count.
// Exported for testing.
func NeedsInvestigation(stat CPUStat) bool {
	if stat.UsedPct >= usedPctWarning {
		return true
	}
	if stat.NumCPU > 0 {
		n := float64(stat.NumCPU)
		if stat.LoadAvg1 > n || stat.LoadAvg5 > n || stat.LoadAvg15 > n {
			return true
		}
	}
	return false
}

// NewPreCheck returns a loop.PreCheck-compatible function that samples CPU
// utilization directly, bypassing the LLM tool-use loop entirely. It reports
// needsLLM=false whenever usage and load are within the responsibility's own
// stated thresholds — the common case on most scheduled ticks — so the loop
// can skip the LLM call outright instead of invoking it just to have it
// discover nothing is wrong.
func NewPreCheck(mem *memory.Store) func(ctx context.Context) (bool, string, error) {
	return func(ctx context.Context) (bool, string, error) {
		stat, report, err := runCheck(ctx, mem)
		if err != nil {
			// Fail open: let the LLM path see and report the failure rather
			// than silently going quiet on a broken health check.
			return true, "", err
		}
		return NeedsInvestigation(stat), report, nil
	}
}

// cpuHealth derives a skillstate health/summary pair from a CPU snapshot:
// critical if usage is at/above usedPctCritical, warning if at/above
// usedPctWarning, else ok.
func cpuHealth(stat CPUStat) (skillstate.Health, string) {
	summary := fmt.Sprintf("CPU used: %.1f%% (%d logical CPUs, load avg %.2f/%.2f/%.2f)",
		stat.UsedPct, stat.NumCPU, stat.LoadAvg1, stat.LoadAvg5, stat.LoadAvg15)
	switch {
	case stat.UsedPct >= usedPctCritical:
		return skillstate.HealthCritical, summary
	case stat.UsedPct >= usedPctWarning:
		return skillstate.HealthWarning, summary
	default:
		return skillstate.HealthOK, summary
	}
}

// BuildReport composes a human-readable summary of CPU utilization.
func BuildReport(stat CPUStat) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "CPU\n")
	fmt.Fprintf(&sb, "  Logical CPUs: %d\n", stat.NumCPU)
	fmt.Fprintf(&sb, "  Usage:        %.1f%%\n", stat.UsedPct)
	if stat.LoadAvg1 > 0 || stat.LoadAvg5 > 0 || stat.LoadAvg15 > 0 {
		fmt.Fprintf(&sb, "  Load avg:     %.2f (1m)  %.2f (5m)  %.2f (15m)\n",
			stat.LoadAvg1, stat.LoadAvg5, stat.LoadAvg15)
	}
	return sb.String()
}
