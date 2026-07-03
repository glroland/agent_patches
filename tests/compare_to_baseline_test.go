package tests

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skills/compare_to_baseline"
	"agent_patches/endpoint-server/utils/config"
)

func TestCompareToBaseline_ReturnsCurrentAndBaselines(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	d := mem.Domain("check_drives")

	now := time.Now()
	// Oldest first so pruning never sees files from its future.
	for _, p := range []struct {
		off  time.Duration
		used int
	}{
		{7 * 24 * time.Hour, 60},
		{24 * time.Hour, 70},
		{time.Hour, 74},
		{0, 75},
	} {
		ts := now.Add(-p.off)
		d.Clock = func() time.Time { return ts }
		if err := d.Write(map[string]int{"used_pct": p.used}); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	tl, err := compare_to_baseline.NewCompareToBaselineTool(mem)
	if err != nil {
		t.Fatalf("NewCompareToBaselineTool: %v", err)
	}

	out, err := tl.Execute(context.Background(), json.RawMessage(`{"domain":"check_drives"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var resp struct {
		Domain  string `json:"domain"`
		Current *struct {
			Data map[string]int `json:"data"`
		} `json:"current"`
		HourAgo *struct {
			Data map[string]int `json:"data"`
		} `json:"hour_ago"`
		DayAgo *struct {
			Data map[string]int `json:"data"`
		} `json:"day_ago"`
		WeekAgo *struct {
			Data map[string]int `json:"data"`
		} `json:"week_ago"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal output %q: %v", out, err)
	}

	if resp.Current == nil || resp.Current.Data["used_pct"] != 75 {
		t.Errorf("current = %+v, want used_pct=75", resp.Current)
	}
	if resp.HourAgo == nil || resp.HourAgo.Data["used_pct"] != 74 {
		t.Errorf("hour_ago = %+v, want used_pct=74", resp.HourAgo)
	}
	if resp.DayAgo == nil || resp.DayAgo.Data["used_pct"] != 70 {
		t.Errorf("day_ago = %+v, want used_pct=70", resp.DayAgo)
	}
	if resp.WeekAgo == nil || resp.WeekAgo.Data["used_pct"] != 60 {
		t.Errorf("week_ago = %+v, want used_pct=60", resp.WeekAgo)
	}
}

func TestCompareToBaseline_OmitsDuplicateBaselinePoints(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	d := mem.Domain("analyze_cpu_utilization")

	// Only one snapshot exists — every baseline would resolve to it, so the
	// response must contain current only.
	if err := d.Write(map[string]int{"cpu_pct": 12}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	tl, err := compare_to_baseline.NewCompareToBaselineTool(mem)
	if err != nil {
		t.Fatalf("NewCompareToBaselineTool: %v", err)
	}
	out, err := tl.Execute(context.Background(), json.RawMessage(`{"domain":"analyze_cpu_utilization"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var resp map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := resp["current"]; !ok {
		t.Error("response missing current")
	}
	for _, k := range []string{"hour_ago", "day_ago", "week_ago"} {
		if _, ok := resp[k]; ok {
			t.Errorf("response contains %s, want it omitted when it duplicates current", k)
		}
	}
}

func TestCompareToBaseline_EmptyDomain_ReturnsErrorPayload(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tl, err := compare_to_baseline.NewCompareToBaselineTool(mem)
	if err != nil {
		t.Fatalf("NewCompareToBaselineTool: %v", err)
	}
	out, err := tl.Execute(context.Background(), json.RawMessage(`{"domain":"nothing_here"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var resp map[string]string
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["error"] == "" {
		t.Errorf("output = %q, want error field for empty domain", out)
	}
}
