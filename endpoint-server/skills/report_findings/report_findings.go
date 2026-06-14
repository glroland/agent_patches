// Package report_findings provides a tool that lets the agent record
// observations, actions, recommendations, and approval requests onto the
// host's activity timeline, surfaced via GET /status.
package report_findings

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/status"
)

// maxEntries caps the number of timeline entries retained.
const maxEntries = 50

// Finding is one entry the agent wants recorded on the activity timeline.
type Finding struct {
	Type           string `json:"type" jsonschema_description:"One of: observation, action, recommendation, approval."`
	Title          string `json:"title" jsonschema_description:"Short one-line summary."`
	Detail         string `json:"detail" jsonschema_description:"Full explanation of the finding."`
	Severity       string `json:"severity,omitempty" jsonschema_description:"One of: info, warning, critical."`
	Risk           string `json:"risk,omitempty" jsonschema_description:"One of: low, medium, high. Set when type=approval."`
	ProposedAction string `json:"proposedAction,omitempty" jsonschema_description:"What the agent proposes to do, when type=approval."`
	Status         string `json:"status,omitempty" jsonschema_description:"One of: pending, approved, rejected, completed. Use pending for new approval requests."`
}

type reportFindingsInput struct {
	Findings []Finding `json:"findings" jsonschema_description:"One or more findings to record for this run."`
}

// NewReportFindingsTool returns a task tool that appends findings to the
// "timeline" memory domain, newest first, capped at maxEntries.
func NewReportFindingsTool(mem *memory.Store) (tool.Tool, error) {
	return tool.New(
		"report_findings",
		"Record one or more findings (observations, actions taken, recommendations, "+
			"or approval requests) discovered during this run. Each finding is added to "+
			"the host's activity timeline shown to the operator. Call this whenever you "+
			"observe something noteworthy, take an action, want to recommend something, "+
			"or need operator approval before proceeding.",
		func(_ context.Context, in reportFindingsInput) (string, error) {
			slog.Info("report_findings: starting", "findings", len(in.Findings))

			d := mem.Domain("timeline")
			var entries []status.TimelineEntry
			_ = d.ReadCurrent(&entries)

			now := time.Now()
			for i, f := range in.Findings {
				entries = append([]status.TimelineEntry{{
					ID:             fmt.Sprintf("%s-%d-%d", f.Type, now.UnixNano(), i),
					Time:           now.Format(time.RFC3339),
					Type:           f.Type,
					Title:          f.Title,
					Detail:         f.Detail,
					Severity:       f.Severity,
					Risk:           f.Risk,
					ProposedAction: ptrOrNil(f.ProposedAction),
					Status:         ptrOrNil(f.Status),
				}}, entries...)
			}
			if len(entries) > maxEntries {
				entries = entries[:maxEntries]
			}

			if err := d.Write(entries); err != nil {
				slog.Info("report_findings: failed", "error", err)
				return "", fmt.Errorf("report_findings: %w", err)
			}

			slog.Info("report_findings: completed", "total_entries", len(entries))
			return fmt.Sprintf("recorded %d finding(s)", len(in.Findings)), nil
		},
	)
}

func ptrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
