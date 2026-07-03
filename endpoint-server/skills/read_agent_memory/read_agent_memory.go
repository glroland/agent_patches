package read_agent_memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/memory"
)

// defaultHistoryWindow bounds how much history is returned when history=true
// and no window is given, so the response stays small enough for the model.
const defaultHistoryWindow = time.Hour

// readMemoryInput is the typed input for the read_agent_memory tool.
type readMemoryInput struct {
	Domain  string `json:"domain"  jsonschema_description:"Memory domain to read, e.g. disk, memory, net_upload, net_download, logins."`
	History bool   `json:"history" jsonschema_description:"If true, return retained snapshots within the window. If false, return only the most recent snapshot."`
	Window  string `json:"window,omitempty" jsonschema_description:"With history=true: how far back to read, as a Go duration string (e.g. \"1h\", \"24h\", \"168h\"). Retention is 5-minute resolution for 1 hour, hourly for 7 days, daily for 90 days. Defaults to \"1h\"."`
}

// NewReadMemoryTool returns a task tool that reads the current snapshot or
// recent history from a named domain of the file-backed memory store.
func NewReadMemoryTool(mem *memory.Store) (tool.Tool, error) {
	return tool.New(
		"read_agent_memory",
		"Read the current snapshot or recent history from a named memory domain. "+
			"Set history=true to get retained snapshots within the given window "+
			"(default \"1h\"). Retention is tiered: 5-minute resolution over the last "+
			"hour, hourly over the last 7 days, daily over the last 90 days — so "+
			"window=\"168h\" returns roughly one snapshot per hour for a week. "+
			"For a compact now-vs-1h-vs-24h-vs-7d comparison prefer compare_to_baseline.",
		func(_ context.Context, in readMemoryInput) (string, error) {
			slog.Info("read_agent_memory: starting", "domain", in.Domain, "history", in.History, "window", in.Window)
			d := mem.Domain(in.Domain)
			if in.History {
				window := defaultHistoryWindow
				if in.Window != "" {
					w, err := time.ParseDuration(in.Window)
					if err != nil || w <= 0 {
						return "", fmt.Errorf("read_agent_memory: invalid window %q: must be a positive Go duration like \"1h\" or \"24h\"", in.Window)
					}
					window = w
				}
				snaps, err := d.ReadHistory()
				if err != nil {
					slog.Info("read_agent_memory: failed", "domain", in.Domain, "error", err)
					return "", fmt.Errorf("read history %q: %w", in.Domain, err)
				}
				cutoff := time.Now().Add(-window)
				filtered := snaps[:0]
				for _, s := range snaps {
					if !s.Timestamp.Before(cutoff) {
						filtered = append(filtered, s)
					}
				}
				snaps = filtered
				if len(snaps) == 0 {
					slog.Info("read_agent_memory: completed", "domain", in.Domain, "snapshots", 0)
					return fmt.Sprintf(`{"domain":%q,"snapshots":[]}`, in.Domain), nil
				}
				type entry struct {
					Timestamp time.Time       `json:"timestamp"`
					Data      json.RawMessage `json:"data"`
				}
				out := make([]entry, len(snaps))
				for i, s := range snaps {
					out[i] = entry{Timestamp: s.Timestamp, Data: s.Data}
				}
				b, _ := json.Marshal(out)
				slog.Info("read_agent_memory: completed", "domain", in.Domain, "snapshots", len(out), "output_len", len(b))
				return string(b), nil
			}

			var raw json.RawMessage
			if err := d.ReadCurrent(&raw); err != nil {
				slog.Info("read_agent_memory: completed", "domain", in.Domain, "result", "no_snapshot")
				return fmt.Sprintf(`{"domain":%q,"error":"no snapshot available"}`, in.Domain), nil
			}
			slog.Info("read_agent_memory: completed", "domain", in.Domain, "output_len", len(raw))
			return string(raw), nil
		},
	)
}
