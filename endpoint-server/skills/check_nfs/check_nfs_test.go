package check_nfs

import (
	"context"
	"testing"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skillstate"
	"agent_patches/endpoint-server/utils/config"
)

func TestAssessMount(t *testing.T) {
	tests := []struct {
		name string
		s    NFSMountStats
		want skillstate.Health
	}{
		{"all clear", NFSMountStats{Mount: "/mnt/a"}, skillstate.HealthOK},
		{"pending ops warning", NFSMountStats{Mount: "/mnt/a", PendingOps: pendingOpsWarning}, skillstate.HealthWarning},
		{"pending ops critical", NFSMountStats{Mount: "/mnt/a", PendingOps: pendingOpsCritical}, skillstate.HealthCritical},
		{"getattr warning", NFSMountStats{Mount: "/mnt/a", GETATTRMs: getattrMsWarning}, skillstate.HealthWarning},
		{"getattr critical", NFSMountStats{Mount: "/mnt/a", GETATTRMs: getattrMsCritical}, skillstate.HealthCritical},
		{"dstate warning", NFSMountStats{Mount: "/mnt/a", DStateProcs: dStateProcWarning}, skillstate.HealthWarning},
		{"dstate critical", NFSMountStats{Mount: "/mnt/a", DStateProcs: dStateProcCritical}, skillstate.HealthCritical},
		{"critical wins over warning", NFSMountStats{Mount: "/mnt/a", PendingOps: pendingOpsCritical, DStateProcs: dStateProcWarning}, skillstate.HealthCritical},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, issue := AssessMount(tt.s)
			if got != tt.want {
				t.Fatalf("AssessMount(%+v) health = %v, want %v (issue=%q)", tt.s, got, tt.want, issue)
			}
			if tt.want == skillstate.HealthOK && issue != "" {
				t.Fatalf("expected no issue text for healthy mount, got %q", issue)
			}
			if tt.want != skillstate.HealthOK && issue == "" {
				t.Fatal("expected issue text for unhealthy mount, got none")
			}
		})
	}
}

// TestNewPreCheck_NoMountsSkipsLLM exercises the gating PreCheck is meant to
// provide: on this test host (no build tag pinning to a live NFS server),
// listNFSMounts returns none, so PreCheck must report needsLLM=false without
// escalating to the caller.
func TestNewPreCheck_NoMountsSkipsLLM(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	pc := NewPreCheck(mem)

	needsLLM, report, err := pc(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if needsLLM {
		t.Fatalf("expected needsLLM=false when no NFS mounts are present, report=%q", report)
	}
	if report != "no NFS mounts found on this host" {
		t.Fatalf("unexpected report: %q", report)
	}
}
