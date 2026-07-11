package tests

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skills/read_agent_memory"
	"agent_patches/endpoint-server/utils/config"
)

func newReadMemoryTool(t *testing.T) (toolExecutor, *memory.Store) {
	t.Helper()
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tl, err := read_agent_memory.NewReadMemoryTool(mem)
	if err != nil {
		t.Fatalf("NewReadMemoryTool: %v", err)
	}
	return tl, mem
}

// toolExecutor is the subset of tool.Tool these tests need.
type toolExecutor interface {
	Execute(ctx context.Context, input json.RawMessage) (string, error)
}

func execReadMemory(t *testing.T, tl toolExecutor, input map[string]any) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(input)
	return tl.Execute(context.Background(), raw)
}

func TestReadAgentMemory_CurrentSnapshot(t *testing.T) {
	tl, mem := newReadMemoryTool(t)
	if err := mem.Domain("check_drives").Write(map[string]any{"disk": "/dev/sda", "used_pct": 42}); err != nil {
		t.Fatal(err)
	}

	out, err := execReadMemory(t, tl, map[string]any{"domain": "check_drives"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, `"used_pct":42`) {
		t.Errorf("output = %s, want current snapshot content", out)
	}
}

func TestReadAgentMemory_NoSnapshot(t *testing.T) {
	tl, _ := newReadMemoryTool(t)

	out, err := execReadMemory(t, tl, map[string]any{"domain": "nothing_here"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "no snapshot available") {
		t.Errorf("output = %s, want no-snapshot message", out)
	}
}

func TestReadAgentMemory_History(t *testing.T) {
	tl, mem := newReadMemoryTool(t)
	d := mem.Domain("analyze_cpu_utilization")
	for i := range 3 {
		if err := d.Write(map[string]int{"sample": i}); err != nil {
			t.Fatal(err)
		}
	}

	out, err := execReadMemory(t, tl, map[string]any{
		"domain": "analyze_cpu_utilization", "history": true, "window": "24h",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var entries []struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("history output not JSON array: %v; out: %s", err, out)
	}
	// Snapshots written in the same 5-minute bucket are pruned to the newest,
	// so at least one — the most recent — must survive.
	if len(entries) == 0 {
		t.Fatal("history returned no snapshots")
	}
	if !strings.Contains(string(entries[len(entries)-1].Data), `"sample":2`) {
		t.Errorf("newest snapshot = %s, want sample 2", entries[len(entries)-1].Data)
	}
}

func TestReadAgentMemory_HistoryEmptyDomain(t *testing.T) {
	tl, _ := newReadMemoryTool(t)

	out, err := execReadMemory(t, tl, map[string]any{
		"domain": "empty_domain", "history": true, "window": "1h",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, `"snapshots":[]`) {
		t.Errorf("output = %s, want empty snapshots list", out)
	}
}

func TestReadAgentMemory_InvalidWindow(t *testing.T) {
	tl, _ := newReadMemoryTool(t)

	for _, window := range []string{"tomorrow", "-1h", "0s"} {
		if _, err := execReadMemory(t, tl, map[string]any{
			"domain": "d", "history": true, "window": window,
		}); err == nil {
			t.Errorf("window %q accepted, want error", window)
		}
	}
}
