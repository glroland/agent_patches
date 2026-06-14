package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skills/report_findings"
	"agent_patches/endpoint-server/status"
	"agent_patches/endpoint-server/utils/config"
)

func TestReportFindingsTool_WritesTimeline(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tool, err := report_findings.NewReportFindingsTool(mem)
	if err != nil {
		t.Fatalf("NewReportFindingsTool: %v", err)
	}

	input, _ := json.Marshal(map[string]any{
		"findings": []map[string]any{
			{"type": "observation", "title": "disk usage high", "detail": "85% full", "severity": "warning"},
			{"type": "approval", "title": "apply patches", "detail": "3 updates available", "risk": "low", "proposedAction": "apt-get upgrade", "status": "pending"},
		},
	})

	if _, err := tool.Execute(context.Background(), input); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var entries []status.TimelineEntry
	if err := mem.Domain("timeline").ReadCurrent(&entries); err != nil {
		t.Fatalf("ReadCurrent: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("entries len = %d, want 2", len(entries))
	}

	// Newest first: the second finding submitted should be entries[0].
	if entries[0].Type != "approval" || entries[0].Title != "apply patches" {
		t.Errorf("entries[0] = %+v, want approval/apply patches", entries[0])
	}
	if entries[0].Status == nil || *entries[0].Status != "pending" {
		t.Errorf("entries[0].Status = %v, want pending", entries[0].Status)
	}
	if entries[0].ProposedAction == nil || *entries[0].ProposedAction != "apt-get upgrade" {
		t.Errorf("entries[0].ProposedAction = %v, want apt-get upgrade", entries[0].ProposedAction)
	}

	if entries[1].Type != "observation" || entries[1].Severity != "warning" {
		t.Errorf("entries[1] = %+v, want observation/warning", entries[1])
	}
}

func TestReportFindingsTool_PrependsAcrossCalls(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tool, err := report_findings.NewReportFindingsTool(mem)
	if err != nil {
		t.Fatalf("NewReportFindingsTool: %v", err)
	}

	for i := 0; i < 3; i++ {
		input, _ := json.Marshal(map[string]any{
			"findings": []map[string]any{
				{"type": "observation", "title": fmt.Sprintf("finding %d", i), "detail": "detail"},
			},
		})
		if _, err := tool.Execute(context.Background(), input); err != nil {
			t.Fatalf("Execute %d: %v", i, err)
		}
	}

	var entries []status.TimelineEntry
	if err := mem.Domain("timeline").ReadCurrent(&entries); err != nil {
		t.Fatalf("ReadCurrent: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries len = %d, want 3", len(entries))
	}
	if entries[0].Title != "finding 2" {
		t.Errorf("entries[0].Title = %q, want %q (newest first)", entries[0].Title, "finding 2")
	}
	if entries[2].Title != "finding 0" {
		t.Errorf("entries[2].Title = %q, want %q (oldest last)", entries[2].Title, "finding 0")
	}
}

func TestReportFindingsTool_CapsAtMaxEntries(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tool, err := report_findings.NewReportFindingsTool(mem)
	if err != nil {
		t.Fatalf("NewReportFindingsTool: %v", err)
	}

	for i := 0; i < 55; i++ {
		input, _ := json.Marshal(map[string]any{
			"findings": []map[string]any{
				{"type": "observation", "title": fmt.Sprintf("finding %d", i), "detail": "detail"},
			},
		})
		if _, err := tool.Execute(context.Background(), input); err != nil {
			t.Fatalf("Execute %d: %v", i, err)
		}
	}

	var entries []status.TimelineEntry
	if err := mem.Domain("timeline").ReadCurrent(&entries); err != nil {
		t.Fatalf("ReadCurrent: %v", err)
	}
	if len(entries) != 50 {
		t.Fatalf("entries len = %d, want 50", len(entries))
	}
	if entries[0].Title != "finding 54" {
		t.Errorf("entries[0].Title = %q, want %q (newest first)", entries[0].Title, "finding 54")
	}
}
