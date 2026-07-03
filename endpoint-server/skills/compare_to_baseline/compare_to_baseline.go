// Package compare_to_baseline provides a skill that returns a memory domain's
// current snapshot alongside the nearest retained snapshots from ~1 hour,
// ~24 hours, and ~7 days ago, so the agent can compare current readings
// against historical baselines (growth rates, anomalies, time-to-full
// predictions) instead of judging a single point in time.
package compare_to_baseline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/memory"
)

// baselineOffsets are the lookback points returned, keyed by response field.
var baselineOffsets = []struct {
	Name   string
	Offset time.Duration
}{
	{"hour_ago", time.Hour},
	{"day_ago", 24 * time.Hour},
	{"week_ago", 7 * 24 * time.Hour},
}

type compareToBaselineInput struct {
	Domain string `json:"domain" jsonschema_description:"Memory domain to compare, e.g. check_drives, analyze_cpu_utilization, analyze_memory_utilization, analyze_network_utilization."`
}

// point is one snapshot in the comparison response.
type point struct {
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// NewCompareToBaselineTool returns a task tool that reads baseline snapshots
// from the tiered memory store for the named domain.
func NewCompareToBaselineTool(mem *memory.Store) (tool.Tool, error) {
	return tool.New(
		"compare_to_baseline",
		"Read a memory domain's current snapshot together with the nearest retained "+
			"snapshots from approximately 1 hour, 24 hours, and 7 days ago. Use this to "+
			"compare current readings against the host's historical baseline: compute "+
			"growth rates (e.g. disk fill rate per day and predicted time-to-full), spot "+
			"anomalies (e.g. network traffic far above the same time last week), and "+
			"distinguish a chronic condition from a new one. Each returned point carries "+
			"its actual timestamp — check it, since a young host may not have a full week "+
			"of history yet. Baseline points that would duplicate an already-returned point are omitted.",
		func(_ context.Context, in compareToBaselineInput) (string, error) {
			if in.Domain == "" {
				return "", fmt.Errorf("compare_to_baseline: domain must not be empty")
			}
			slog.Info("compare_to_baseline: starting", "domain", in.Domain)

			d := mem.Domain(in.Domain)
			now := time.Now()

			current, err := d.ReadNearest(now)
			if err != nil {
				slog.Info("compare_to_baseline: completed", "domain", in.Domain, "result", "no_snapshots")
				return fmt.Sprintf(`{"domain":%q,"error":"no snapshots available for this domain"}`, in.Domain), nil
			}

			out := map[string]any{
				"domain":  in.Domain,
				"current": point{Timestamp: current.Timestamp, Data: current.Data},
			}
			seen := map[int64]bool{current.Timestamp.UnixNano(): true}
			for _, b := range baselineOffsets {
				snap, err := d.ReadNearest(now.Add(-b.Offset))
				if err != nil || seen[snap.Timestamp.UnixNano()] {
					continue
				}
				seen[snap.Timestamp.UnixNano()] = true
				out[b.Name] = point{Timestamp: snap.Timestamp, Data: snap.Data}
			}

			b, err := json.Marshal(out)
			if err != nil {
				return "", fmt.Errorf("compare_to_baseline: marshal: %w", err)
			}
			slog.Info("compare_to_baseline: completed", "domain", in.Domain, "output_len", len(b))
			return string(b), nil
		},
	)
}
