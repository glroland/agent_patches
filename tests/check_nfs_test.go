package tests

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skills/check_nfs"
	"agent_patches/endpoint-server/skillstate"
	"agent_patches/endpoint-server/utils/config"
)

// sampleMountstats is a representative /proc/self/mountstats excerpt with two
// NFS mounts: one healthy (nas:/data) and one with a high pending-ops count
// (nas:/backup).
const sampleMountstats = `device rootfs mounted on / with fstype rootfs
device sysfs mounted on /sys with fstype sysfs
device nas:/data mounted on /mnt/data with fstype nfs4 statvers 1.1
  opts:   rw,vers=4.1
  age:    3600
  xprt:   tcp 0 0 1 0 14 4294967295 0 0 10 20 128 5 50
  per-op statistics
  NULL: 0 0 0 0 0 0 0 0
  GETATTR: 1000 1000 0 128000 160000 500000 200000 200000
device nas:/backup mounted on /mnt/backup with fstype nfs4 statvers 1.1
  opts:   rw,vers=4.1
  age:    1800
  xprt:   tcp 0 0 1 0 14 4294967295 0 0 50 100 128 25 2100
  per-op statistics
  NULL: 0 0 0 0 0 0 0 0
  GETATTR: 500 500 0 64000 80000 3000000 6000000 6000000
`

// --- ParseMountstats ---

func TestParseMountstats_FindsNFSMounts(t *testing.T) {
	result := check_nfs.ParseMountstats(sampleMountstats)
	if _, ok := result["/mnt/data"]; !ok {
		t.Error("ParseMountstats: missing /mnt/data")
	}
	if _, ok := result["/mnt/backup"]; !ok {
		t.Error("ParseMountstats: missing /mnt/backup")
	}
	if _, ok := result["/"]; ok {
		t.Error("ParseMountstats: included rootfs (non-NFS) mount")
	}
}

func TestParseMountstats_PendingOps(t *testing.T) {
	result := check_nfs.ParseMountstats(sampleMountstats)
	data := result["/mnt/data"]
	if data.PendingOps != 50 {
		t.Errorf("/mnt/data PendingOps = %d, want 50", data.PendingOps)
	}
	backup := result["/mnt/backup"]
	if backup.PendingOps != 2100 {
		t.Errorf("/mnt/backup PendingOps = %d, want 2100", backup.PendingOps)
	}
}

func TestParseMountstats_GETATTRLatency(t *testing.T) {
	result := check_nfs.ParseMountstats(sampleMountstats)
	// /mnt/data: cum_rtt=200000ms / 1000 ops = 200ms avg
	data := result["/mnt/data"]
	if data.GETATTRMs != 200.0 {
		t.Errorf("/mnt/data GETATTRMs = %.1f, want 200.0", data.GETATTRMs)
	}
	// /mnt/backup: 6000000ms / 500 ops = 12000ms avg
	backup := result["/mnt/backup"]
	if backup.GETATTRMs != 12000.0 {
		t.Errorf("/mnt/backup GETATTRMs = %.1f, want 12000.0", backup.GETATTRMs)
	}
}

func TestParseMountstats_EmptyData(t *testing.T) {
	if got := check_nfs.ParseMountstats(""); len(got) != 0 {
		t.Errorf("ParseMountstats(\"\") = %v, want empty map", got)
	}
}

func TestParseMountstats_NoNFSMounts(t *testing.T) {
	data := `device rootfs mounted on / with fstype rootfs
device tmpfs mounted on /tmp with fstype tmpfs
`
	if got := check_nfs.ParseMountstats(data); len(got) != 0 {
		t.Errorf("ParseMountstats with no NFS = %v, want empty", got)
	}
}

// --- AssessMount ---

func TestAssessMount_Healthy(t *testing.T) {
	s := check_nfs.NFSMountStats{Mount: "/mnt/data", PendingOps: 10, GETATTRMs: 50, DStateProcs: 0}
	h, issue := check_nfs.AssessMount(s)
	if h != skillstate.HealthOK {
		t.Errorf("healthy mount → %q, want ok", h)
	}
	if issue != "" {
		t.Errorf("healthy mount issue = %q, want empty", issue)
	}
}

func TestAssessMount_PendingOpsWarning(t *testing.T) {
	s := check_nfs.NFSMountStats{Mount: "/mnt/data", PendingOps: 700}
	h, issue := check_nfs.AssessMount(s)
	if h != skillstate.HealthWarning {
		t.Errorf("500<ops<2000 → %q, want warning", h)
	}
	if !strings.Contains(issue, "pending ops") {
		t.Errorf("issue %q missing 'pending ops'", issue)
	}
}

func TestAssessMount_PendingOpsCritical(t *testing.T) {
	s := check_nfs.NFSMountStats{Mount: "/mnt/data", PendingOps: 2500}
	h, issue := check_nfs.AssessMount(s)
	if h != skillstate.HealthCritical {
		t.Errorf("ops>=2000 → %q, want critical", h)
	}
	if !strings.Contains(issue, "critical") {
		t.Errorf("issue %q missing 'critical'", issue)
	}
}

func TestAssessMount_GETATTRWarning(t *testing.T) {
	s := check_nfs.NFSMountStats{Mount: "/mnt/data", GETATTRMs: 1500}
	h, _ := check_nfs.AssessMount(s)
	if h != skillstate.HealthWarning {
		t.Errorf("1000<getattr<5000ms → %q, want warning", h)
	}
}

func TestAssessMount_GETATTRCritical(t *testing.T) {
	s := check_nfs.NFSMountStats{Mount: "/mnt/data", GETATTRMs: 6000}
	h, _ := check_nfs.AssessMount(s)
	if h != skillstate.HealthCritical {
		t.Errorf("getattr>=5000ms → %q, want critical", h)
	}
}

func TestAssessMount_DStateProcWarning(t *testing.T) {
	s := check_nfs.NFSMountStats{Mount: "/mnt/data", DStateProcs: 2}
	h, _ := check_nfs.AssessMount(s)
	if h != skillstate.HealthWarning {
		t.Errorf("1<=D-state<5 → %q, want warning", h)
	}
}

func TestAssessMount_DStateProcCritical(t *testing.T) {
	s := check_nfs.NFSMountStats{Mount: "/mnt/data", DStateProcs: 6}
	h, _ := check_nfs.AssessMount(s)
	if h != skillstate.HealthCritical {
		t.Errorf("D-state>=5 → %q, want critical", h)
	}
}

func TestAssessMount_WorstSeverityWins(t *testing.T) {
	// Warning latency + critical pending ops → critical overall.
	s := check_nfs.NFSMountStats{Mount: "/mnt/data", PendingOps: 3000, GETATTRMs: 1500}
	h, _ := check_nfs.AssessMount(s)
	if h != skillstate.HealthCritical {
		t.Errorf("mixed severities → %q, want critical", h)
	}
}

// --- Tool construction ---

func TestNewCheckNFSTool_NameAndDescription(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tl, err := check_nfs.NewCheckNFSTool(mem)
	if err != nil {
		t.Fatalf("NewCheckNFSTool() error: %v", err)
	}
	if got := tl.Name(); got != "check_nfs" {
		t.Errorf("Name() = %q, want check_nfs", got)
	}
	if tl.Description() == "" {
		t.Error("Description() returned empty string")
	}
}

func TestCheckNFSTool_Execute_NoMounts(t *testing.T) {
	// On the test host (macOS/Linux dev) there are likely no NFS mounts.
	// The skill should handle this gracefully and not error.
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tl, err := check_nfs.NewCheckNFSTool(mem)
	if err != nil {
		t.Fatalf("NewCheckNFSTool() error: %v", err)
	}

	input, _ := json.Marshal(struct{}{})
	result, err := tl.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if result == "" {
		t.Error("Execute() returned empty result")
	}

	// Skillstate should always be written.
	states, err := skillstate.LoadAll(mem)
	if err != nil {
		t.Fatalf("skillstate.LoadAll: %v", err)
	}
	if len(states) != 1 || states[0].Skill != "check_nfs" {
		t.Fatalf("skillstate = %+v, want one entry for check_nfs", states)
	}
}
