package analyze_network_utilization

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"agent_patches/endpoint-server/a2a/tool"
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
// short interval.
func NewNetworkUsageTool() (tool.Tool, error) {
	return tool.New(
		"analyze_network_utilization",
		"Reports current network upload and download rates (in MB/s) for the "+
			"host, summed across all non-loopback interfaces. Takes a brief "+
			"sample over the requested duration to compute the rate.",
		func(ctx context.Context, in networkUsageInput) (string, error) {
			slog.Info("analyze_network_utilization: starting", "requested_duration_seconds", in.DurationSeconds)

			d := in.DurationSeconds
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
				return "", fmt.Errorf("network_usage: %w", err)
			}
			slog.Debug("analyze_network_utilization: initial snapshot", "in_bytes", inBytes1, "out_bytes", outBytes1)

			select {
			case <-ctx.Done():
				slog.Info("analyze_network_utilization: cancelled", "error", ctx.Err())
				return "", ctx.Err()
			case <-time.After(time.Duration(d * float64(time.Second))):
			}

			inBytes2, outBytes2, err := snapshot()
			if err != nil {
				slog.Info("analyze_network_utilization: failed", "error", err)
				return "", fmt.Errorf("network_usage: %w", err)
			}
			slog.Debug("analyze_network_utilization: final snapshot", "in_bytes", inBytes2, "out_bytes", outBytes2)

			var downRate, upRate float64
			if inBytes2 >= inBytes1 {
				downRate = float64(inBytes2-inBytes1) / d / (1024 * 1024)
			}
			if outBytes2 >= outBytes1 {
				upRate = float64(outBytes2-outBytes1) / d / (1024 * 1024)
			}

			report := BuildReport(downRate, upRate)
			slog.Info("analyze_network_utilization: completed",
				"download_mbps", fmt.Sprintf("%.2f", downRate), "upload_mbps", fmt.Sprintf("%.2f", upRate))
			return report, nil
		},
	)
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
