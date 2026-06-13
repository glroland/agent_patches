package tests

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agent_patches/endpoint-server/skills/analyze_disk_utilization"
)

func TestDiskStat_UsedPct_Normal(t *testing.T) {
	d := analyze_disk_utilization.DiskStat{Total: 100, Free: 20}
	if got := d.UsedPct(); got != 80.0 {
		t.Errorf("UsedPct() = %.2f, want 80.00", got)
	}
}

func TestDiskStat_UsedPct_ZeroTotal(t *testing.T) {
	d := analyze_disk_utilization.DiskStat{Total: 0, Free: 0}
	if got := d.UsedPct(); got != 0 {
		t.Errorf("UsedPct() with zero total = %.2f, want 0", got)
	}
}

func TestDiskStat_Used_Underflow(t *testing.T) {
	d := analyze_disk_utilization.DiskStat{Total: 10, Free: 20}
	if got := d.Used(); got != 0 {
		t.Errorf("Used() with Free>Total = %d, want 0", got)
	}
}

func TestDiskUsage_BuildReport_ContainsEssentialFields(t *testing.T) {
	disks := []analyze_disk_utilization.DiskStat{
		{Mount: "/data", Total: 100 << 30, Free: 5 << 30, FSType: "ext4"},
	}
	report := analyze_disk_utilization.BuildReport(disks)

	for _, want := range []string{"/data", "ext4", "95.0%", "100.00 GB", "5.00 GB"} {
		if !strings.Contains(report, want) {
			t.Errorf("BuildReport missing %q\nfull report:\n%s", want, report)
		}
	}
}

func TestDiskUsage_BuildReport_NoFSType(t *testing.T) {
	disks := []analyze_disk_utilization.DiskStat{
		{Mount: `C:\`, Total: 500 << 30, Free: 10 << 30},
	}
	report := analyze_disk_utilization.BuildReport(disks)

	if !strings.Contains(report, `C:\`) {
		t.Errorf("BuildReport should contain drive letter:\n%s", report)
	}
	if strings.Contains(report, "Filesystem:") {
		t.Errorf("BuildReport should omit Filesystem line when FSType is empty:\n%s", report)
	}
}

func TestNewDiskUsageTool_NameAndDescription(t *testing.T) {
	tl, err := analyze_disk_utilization.NewDiskUsageTool()
	if err != nil {
		t.Fatalf("NewDiskUsageTool() unexpected error: %v", err)
	}
	if got := tl.Name(); got != "analyze_disk_utilization" {
		t.Errorf("Name() = %q, want %q", got, "analyze_disk_utilization")
	}
	if tl.Description() == "" {
		t.Error("Description() returned empty string")
	}
}

func TestDiskUsageTool_Execute_ReturnsReport(t *testing.T) {
	tl, err := analyze_disk_utilization.NewDiskUsageTool()
	if err != nil {
		t.Fatalf("NewDiskUsageTool() unexpected error: %v", err)
	}

	input, _ := json.Marshal(struct{}{})
	result, err := tl.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	if result == "" {
		t.Error("Execute() returned empty result")
	}
}
