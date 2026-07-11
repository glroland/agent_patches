package analyze_network_utilization

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skillstate"
)

const (
	defaultSampleSeconds = 1.0
	minSampleSeconds     = 0.1
	maxSampleSeconds     = 10.0
)

type networkUsageInput struct {
	DurationSeconds float64 `json:"duration_seconds,omitempty" jsonschema_description:"How long to sample network counters for, in seconds, used to compute the current rate. Defaults to 1; clamped between 0.1 and 10."`
}

// NewNetworkUsageTool returns a task tool that reports current upload and
// download rates for the host by sampling network counters twice over a
// short interval. The result is also recorded as the skill's last known
// state (see skillstate), surfaced in GET /status.
func NewNetworkUsageTool(mem *memory.Store) (tool.Tool, error) {
	return tool.New(
		"analyze_network_utilization",
		"Reports current network upload and download rates (in MB/s) for the "+
			"host, summed across all non-loopback interfaces. Takes a brief "+
			"sample over the requested duration to compute the rate.",
		func(ctx context.Context, in networkUsageInput) (string, error) {
			slog.Info("analyze_network_utilization: starting", "requested_duration_seconds", in.DurationSeconds)
			downRate, upRate, err := sampleRates(ctx, mem, in.DurationSeconds)
			if err != nil {
				return "", err
			}
			return BuildReport(downRate, upRate), nil
		},
	)
}

// sampleRates measures throughput over the requested duration (clamped),
// records the result as the skill's last known state, and returns the rates
// in MB/s. It makes no LLM calls — callers decide whether the result
// warrants one.
func sampleRates(ctx context.Context, mem *memory.Store, durationSeconds float64) (downRate, upRate float64, err error) {
	d := durationSeconds
	if d <= 0 {
		d = defaultSampleSeconds
	}
	if d < minSampleSeconds {
		d = minSampleSeconds
	}
	if d > maxSampleSeconds {
		d = maxSampleSeconds
	}
	slog.Debug("analyze_network_utilization: sample duration clamped", "duration_seconds", d)

	inBytes1, outBytes1, err := snapshot()
	if err != nil {
		slog.Info("analyze_network_utilization: failed", "error", err)
		_ = skillstate.Save(mem, "analyze_network_utilization", skillstate.HealthCritical, fmt.Sprintf("failed to read network counters: %v", err))
		return 0, 0, fmt.Errorf("network_usage: %w", err)
	}
	slog.Debug("analyze_network_utilization: initial snapshot", "in_bytes", inBytes1, "out_bytes", outBytes1)

	select {
	case <-ctx.Done():
		slog.Info("analyze_network_utilization: cancelled", "error", ctx.Err())
		return 0, 0, ctx.Err()
	case <-time.After(time.Duration(d * float64(time.Second))):
	}

	inBytes2, outBytes2, err := snapshot()
	if err != nil {
		slog.Info("analyze_network_utilization: failed", "error", err)
		_ = skillstate.Save(mem, "analyze_network_utilization", skillstate.HealthCritical, fmt.Sprintf("failed to read network counters: %v", err))
		return 0, 0, fmt.Errorf("network_usage: %w", err)
	}
	slog.Debug("analyze_network_utilization: final snapshot", "in_bytes", inBytes2, "out_bytes", outBytes2)

	if inBytes2 >= inBytes1 {
		downRate = float64(inBytes2-inBytes1) / d / (1024 * 1024)
	}
	if outBytes2 >= outBytes1 {
		upRate = float64(outBytes2-outBytes1) / d / (1024 * 1024)
	}

	slog.Info("analyze_network_utilization: completed",
		"download_mbps", fmt.Sprintf("%.2f", downRate), "upload_mbps", fmt.Sprintf("%.2f", upRate))
	summary := fmt.Sprintf("Download: %s, Upload: %s", formatBandwidth(downRate), formatBandwidth(upRate))
	_ = skillstate.Save(mem, "analyze_network_utilization", skillstate.HealthOK, summary)
	return downRate, upRate, nil
}

// preCheckSampleSeconds is the sampling window for scheduled pre-checks —
// longer than the tool's 1s default for a steadier reading, since nothing is
// waiting on the answer.
const preCheckSampleSeconds = 5.0

// NewPreCheck returns a loop.PreCheck-compatible function that samples
// network throughput directly, bypassing the LLM tool-use loop entirely. It
// compares the reading against the host's own rolling 7-day baseline (see
// AnomalyIssue) and reports needsLLM=false when traffic is within it — the
// common case on most scheduled ticks — so the loop can skip the LLM call
// outright. Until enough baseline history accumulates, every run escalates
// to the LLM, matching the pre-baseline behaviour.
func NewPreCheck(mem *memory.Store) func(ctx context.Context) (bool, string, error) {
	return func(ctx context.Context) (bool, string, error) {
		downRate, upRate, err := sampleRates(ctx, mem, preCheckSampleSeconds)
		if err != nil {
			// Fail open: let the LLM path see and report the failure rather
			// than silently going quiet on a broken health check.
			return true, "", err
		}

		samples, appended, berr := RecordSample(mem, downRate, upRate, time.Now())
		if berr != nil {
			slog.Warn("analyze_network_utilization: baseline recording failed", "error", berr)
		}
		// Judge against the history excluding the reading just recorded, so
		// the current spike can't drag the median toward itself.
		history := samples
		if appended {
			history = history[:len(history)-1]
		}

		report := BuildReport(downRate, upRate)
		if issue := AnomalyIssue(history, downRate, upRate); issue != "" {
			return true, report + "Anomaly: " + issue + "\n", nil
		}
		return false, report + "Traffic is within this host's 7-day baseline.", nil
	}
}

// BuildReport composes a human-readable summary of network throughput.
func BuildReport(downRateMBps, upRateMBps float64) string {
	return fmt.Sprintf(
		"Download: %s\nUpload:   %s\n",
		formatBandwidth(downRateMBps), formatBandwidth(upRateMBps),
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
