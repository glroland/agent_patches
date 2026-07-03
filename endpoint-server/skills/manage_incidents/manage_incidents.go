// Package manage_incidents exposes the incident ledger to the agent as a
// tool, so problems discovered in one responsibility run are remembered,
// deduplicated, and eventually resolved in later runs instead of being
// rediscovered from scratch every cycle.
package manage_incidents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/incidents"
)

type manageIncidentsInput struct {
	Action      string `json:"action" jsonschema_description:"One of: list, report, log_action, resolve."`
	Fingerprint string `json:"fingerprint,omitempty" jsonschema_description:"Stable kebab-case identifier for the problem, e.g. 'disk-full-var' or 'high-cpu-chrome'. Required for report, log_action, and resolve. Use the same fingerprint every time the same underlying problem is involved."`
	Title       string `json:"title,omitempty" jsonschema_description:"Short one-line summary of the problem. Required when reporting a new incident."`
	Detail      string `json:"detail,omitempty" jsonschema_description:"Fuller description of the problem: affected resource, observed values, suspected cause."`
	Severity    string `json:"severity,omitempty" jsonschema_description:"One of: info, warning, critical."`
	Note        string `json:"note,omitempty" jsonschema_description:"For log_action: the action taken or notable update. For resolve: how/why the problem cleared."`
}

// NewManageIncidentsTool returns a task tool backed by the shared incident store.
func NewManageIncidentsTool(store *incidents.Store) (tool.Tool, error) {
	return tool.New(
		"manage_incidents",
		"Read and update the host's incident ledger — the durable record of ongoing "+
			"problems that persists across runs. Actions: "+
			"'list' returns all incidents (open and recently resolved). "+
			"'report' opens a new incident or, if the fingerprint already exists, marks it "+
			"seen again (bumping last-seen and occurrence count) — always report against an "+
			"existing fingerprint rather than opening a duplicate for the same problem. "+
			"'log_action' appends a note recording an action taken or a significant update. "+
			"'resolve' closes an incident that is no longer occurring, with a resolution note. "+
			"Use incidents for persistent problems worth tracking across runs (a filling disk, "+
			"a runaway process, a failing drive), not for routine healthy check results.",
		func(_ context.Context, in manageIncidentsInput) (string, error) {
			slog.Info("manage_incidents: starting", "action", in.Action, "fingerprint", in.Fingerprint)

			switch in.Action {
			case "list":
				all, err := store.All()
				if err != nil {
					return "", fmt.Errorf("manage_incidents: list: %w", err)
				}
				if len(all) == 0 {
					return `{"incidents":[]}`, nil
				}
				b, err := json.Marshal(map[string]any{"incidents": all})
				if err != nil {
					return "", fmt.Errorf("manage_incidents: marshal: %w", err)
				}
				return string(b), nil

			case "report":
				if in.Fingerprint == "" {
					return "", fmt.Errorf("manage_incidents: report requires a fingerprint")
				}
				if in.Title == "" {
					return "", fmt.Errorf("manage_incidents: report requires a title")
				}
				inc, isNew, err := store.Report(in.Fingerprint, in.Title, in.Detail, in.Severity)
				if err != nil {
					return "", fmt.Errorf("manage_incidents: report: %w", err)
				}
				if isNew {
					slog.Info("manage_incidents: opened incident", "fingerprint", inc.Fingerprint)
					return fmt.Sprintf("opened new incident %q", inc.Fingerprint), nil
				}
				slog.Info("manage_incidents: updated incident", "fingerprint", inc.Fingerprint, "times_seen", inc.TimesSeen)
				return fmt.Sprintf("incident %q already open — recorded recurrence (first seen %s, now seen %d times)",
					inc.Fingerprint, inc.FirstSeen, inc.TimesSeen), nil

			case "log_action":
				if in.Fingerprint == "" || in.Note == "" {
					return "", fmt.Errorf("manage_incidents: log_action requires fingerprint and note")
				}
				inc, err := store.LogAction(in.Fingerprint, in.Note)
				if err != nil {
					return "", fmt.Errorf("manage_incidents: %w", err)
				}
				return fmt.Sprintf("logged action on incident %q (%d actions recorded)", inc.Fingerprint, len(inc.Actions)), nil

			case "resolve":
				if in.Fingerprint == "" {
					return "", fmt.Errorf("manage_incidents: resolve requires a fingerprint")
				}
				inc, err := store.Resolve(in.Fingerprint, in.Note)
				if err != nil {
					return "", fmt.Errorf("manage_incidents: %w", err)
				}
				return fmt.Sprintf("resolved incident %q (open since %s, seen %d times)",
					inc.Fingerprint, inc.FirstSeen, inc.TimesSeen), nil

			default:
				return "", fmt.Errorf("manage_incidents: unknown action %q — use list, report, log_action, or resolve", in.Action)
			}
		},
	)
}
