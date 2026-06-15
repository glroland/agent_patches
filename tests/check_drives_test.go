package tests

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skills/check_drives"
	"agent_patches/endpoint-server/skillstate"
	"agent_patches/endpoint-server/utils/config"
)

func TestDiskStat_UsedPct_Normal(t *testing.T) {
	d := check_drives.DiskStat{Total: 100, Free: 20}
	if got := d.UsedPct(); got != 80.0 {
		t.Errorf("UsedPct() = %.2f, want 80.00", got)
	}
}

func TestDiskStat_UsedPct_ZeroTotal(t *testing.T) {
	d := check_drives.DiskStat{Total: 0, Free: 0}
	if got := d.UsedPct(); got != 0 {
		t.Errorf("UsedPct() with zero total = %.2f, want 0", got)
	}
}

func TestDiskStat_Used_Underflow(t *testing.T) {
	d := check_drives.DiskStat{Total: 10, Free: 20}
	if got := d.Used(); got != 0 {
		t.Errorf("Used() with Free>Total = %d, want 0", got)
	}
}

func TestDiskUsage_BuildReport_ContainsEssentialFields(t *testing.T) {
	disks := []check_drives.DiskStat{
		{Mount: "/data", Total: 100 << 30, Free: 5 << 30, FSType: "ext4"},
	}
	report := check_drives.BuildReport(disks)

	for _, want := range []string{"/data", "ext4", "95.0%", "100.00 GB", "5.00 GB"} {
		if !strings.Contains(report, want) {
			t.Errorf("BuildReport missing %q\nfull report:\n%s", want, report)
		}
	}
}

func TestDiskUsage_BuildReport_NoFSType(t *testing.T) {
	disks := []check_drives.DiskStat{
		{Mount: `C:\`, Total: 500 << 30, Free: 10 << 30},
	}
	report := check_drives.BuildReport(disks)

	if !strings.Contains(report, `C:\`) {
		t.Errorf("BuildReport should contain drive letter:\n%s", report)
	}
	if strings.Contains(report, "Filesystem:") {
		t.Errorf("BuildReport should omit Filesystem line when FSType is empty:\n%s", report)
	}
}

func TestDedupeDisks_CollapsesIdenticalTotalsAndDropsDevfs(t *testing.T) {
	disks := []check_drives.DiskStat{
		{Mount: "/", Total: 100, Free: 10, FSType: "apfs"},
		{Mount: "/System/Volumes/Data", Total: 100, Free: 10, FSType: "apfs"},
		{Mount: "/System/Volumes/VM", Total: 100, Free: 10, FSType: "apfs"},
		{Mount: "/dev", Total: 1, Free: 0, FSType: "devfs"},
		{Mount: "/data", Total: 200, Free: 50, FSType: "ext4"},
	}

	got := check_drives.DedupeDisks(disks)

	if len(got) != 2 {
		t.Fatalf("DedupeDisks() returned %d disks, want 2: %+v", len(got), got)
	}
	if got[0].Mount != "/" || got[1].Mount != "/data" {
		t.Errorf("DedupeDisks() = %+v, want first two unique mounts preserved in order", got)
	}
}

func TestDedupeDisks_CollapsesNearlyIdenticalFreeSpace(t *testing.T) {
	// Mirrors filesystems that share an underlying storage pool (APFS
	// containers on macOS, btrfs/LVM pools on Linux): same total capacity,
	// but free space differs slightly per mount due to per-volume accounting.
	const total = 494384795648
	disks := []check_drives.DiskStat{
		{Mount: "/System/Volumes/VM", Total: total, Free: 46967631872, FSType: "apfs"},
		{Mount: "/", Total: total, Free: 46980497408, FSType: "apfs"},
		{Mount: "/System/Volumes/Data", Total: total, Free: 46974603264, FSType: "apfs"},
	}

	got := check_drives.DedupeDisks(disks)

	if len(got) != 1 {
		t.Fatalf("DedupeDisks() returned %d disks, want 1: %+v", len(got), got)
	}
	if got[0].Mount != "/" {
		t.Errorf("DedupeDisks() kept mount %q, want the preferred mount %q", got[0].Mount, "/")
	}
}

func TestDedupeDisks_KeepsDistinctFilesystems(t *testing.T) {
	disks := []check_drives.DiskStat{
		{Mount: "/", Total: 100 << 30, Free: 5 << 30, FSType: "ext4"},
		{Mount: "/boot/efi", Total: 500 << 20, Free: 400 << 20, FSType: "vfat"},
		{Mount: "/data", Total: 1 << 40, Free: 900 << 30, FSType: "xfs"},
	}

	got := check_drives.DedupeDisks(disks)

	if len(got) != len(disks) {
		t.Fatalf("DedupeDisks() returned %d disks, want %d (no collapsing): %+v", len(got), len(disks), got)
	}
}

func TestNewDiskUsageTool_NameAndDescription(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tl, err := check_drives.NewDiskUsageTool(mem)
	if err != nil {
		t.Fatalf("NewDiskUsageTool() unexpected error: %v", err)
	}
	if got := tl.Name(); got != "check_drives" {
		t.Errorf("Name() = %q, want %q", got, "check_drives")
	}
	if tl.Description() == "" {
		t.Error("Description() returned empty string")
	}
}

func TestDiskUsageTool_Execute_ReturnsReport(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tl, err := check_drives.NewDiskUsageTool(mem)
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

	states, err := skillstate.LoadAll(mem)
	if err != nil {
		t.Fatalf("skillstate.LoadAll: %v", err)
	}
	if len(states) != 1 || states[0].Skill != "check_drives" {
		t.Fatalf("skillstate after Execute() = %+v, want one entry for check_drives", states)
	}
	switch states[0].Health {
	case skillstate.HealthOK, skillstate.HealthWarning, skillstate.HealthCritical:
	default:
		t.Errorf("skillstate health = %q, want ok/warning/critical", states[0].Health)
	}
	if states[0].Summary == "" {
		t.Error("skillstate summary is empty")
	}
}
