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

// readMemoryInput is the typed input for the read_agent_memory tool.
type readMemoryInput struct {
	Domain  string `json:"domain"  jsonschema_description:"Memory domain to read, e.g. disk, memory, net_upload, net_download, logins."`
	History bool   `json:"history" jsonschema_description:"If true, return all retained snapshots (up to 60 min of history). If false, return only the most recent snapshot."`
}

// NewReadMemoryTool returns a task tool that reads the current snapshot or
// full history from a named domain of the file-backed memory store.
func NewReadMemoryTool(mem *memory.Store) (tool.Tool, error) {
	return tool.New(
		"read_agent_memory",
		"Read the current snapshot or full history from a named memory domain. "+
			"Set history=true to get all retained snapshots (one per 5-minute bucket "+
			"over the last 60 minutes); otherwise the most recent snapshot is returned.",
		func(_ context.Context, in readMemoryInput) (string, error) {
			slog.Info("read_agent_memory: starting", "domain", in.Domain, "history", in.History)
			d := mem.Domain(in.Domain)
			if in.History {
				snaps, err := d.ReadHistory()
				if err != nil {
					slog.Info("read_agent_memory: failed", "domain", in.Domain, "error", err)
					return "", fmt.Errorf("read history %q: %w", in.Domain, err)
				}
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
