package tests

import (
	"context"
	"strings"
	"testing"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skills/check_security_posture"
	"agent_patches/endpoint-server/skillstate"
	"agent_patches/endpoint-server/utils/config"
)

func postureSnapshotA() check_security_posture.Snapshot {
	return check_security_posture.Snapshot{
		ListeningPorts: []string{"tcp 0.0.0.0:22 (sshd)", "tcp 127.0.0.1:9976 (patches)"},
		Users:          []string{"lee (uid 1000)", "root (uid 0)"},
		AdminUsers:     []string{"lee", "root"},
		SudoersHash:    "abc123",
		AuthorizedKeys: map[string]string{"lee": "sha256:aaa"},
		SetuidBinaries: []string{"/usr/bin/sudo", "/usr/bin/passwd"},
	}
}

func TestDiffSnapshots_NoChanges(t *testing.T) {
	d := check_security_posture.DiffSnapshots(postureSnapshotA(), postureSnapshotA())
	if !d.Empty() {
		t.Errorf("Diff = %+v, want empty for identical snapshots", d)
	}
}

func TestDiffSnapshots_DetectsAllChangeClasses(t *testing.T) {
	prev := postureSnapshotA()
	cur := check_security_posture.Snapshot{
		ListeningPorts: []string{"tcp 0.0.0.0:22 (sshd)", "tcp 0.0.0.0:4444 (nc)"},
		Users:          []string{"lee (uid 1000)", "root (uid 0)", "eve (uid 1001)"},
		AdminUsers:     []string{"lee", "root", "eve"},
		SudoersHash:    "def456",
		AuthorizedKeys: map[string]string{"lee": "sha256:bbb", "eve": "sha256:ccc"},
		SetuidBinaries: []string{"/usr/bin/sudo", "/usr/bin/passwd", "/tmp/backdoor"},
	}

	d := check_security_posture.DiffSnapshots(prev, cur)
	if d.Empty() {
		t.Fatal("Diff is empty, want changes")
	}
	if len(d.AddedPorts) != 1 || d.AddedPorts[0] != "tcp 0.0.0.0:4444 (nc)" {
		t.Errorf("AddedPorts = %v", d.AddedPorts)
	}
	if len(d.RemovedPorts) != 1 || d.RemovedPorts[0] != "tcp 127.0.0.1:9976 (patches)" {
		t.Errorf("RemovedPorts = %v", d.RemovedPorts)
	}
	if len(d.AddedUsers) != 1 || d.AddedUsers[0] != "eve (uid 1001)" {
		t.Errorf("AddedUsers = %v", d.AddedUsers)
	}
	if len(d.AddedAdmins) != 1 || d.AddedAdmins[0] != "eve" {
		t.Errorf("AddedAdmins = %v", d.AddedAdmins)
	}
	if !d.SudoersChanged {
		t.Error("SudoersChanged = false, want true")
	}
	// lee's key changed, eve's appeared.
	if len(d.ChangedAuthorizedKeys) != 2 {
		t.Errorf("ChangedAuthorizedKeys = %v, want [eve lee]", d.ChangedAuthorizedKeys)
	}
	if len(d.AddedSetuid) != 1 || d.AddedSetuid[0] != "/tmp/backdoor" {
		t.Errorf("AddedSetuid = %v", d.AddedSetuid)
	}
}

func TestCheckSecurityPostureTool_BaselineThenDrift(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})

	snap := postureSnapshotA()
	gatherer := func(context.Context) check_security_posture.Snapshot { return snap }
	tl, err := check_security_posture.NewWithGatherer(mem, gatherer)
	if err != nil {
		t.Fatalf("NewWithGatherer: %v", err)
	}

	// First run establishes the baseline.
	out, err := tl.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute 1: %v", err)
	}
	if !strings.Contains(out, "establishes the baseline") {
		t.Errorf("first run output = %q, want baseline message", out)
	}

	// Second run with no changes.
	out, err = tl.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute 2: %v", err)
	}
	if !strings.Contains(out, "No changes detected") {
		t.Errorf("second run output = %q, want no-changes message", out)
	}

	// Third run: a new port and a new setuid binary appear.
	snap = postureSnapshotA()
	snap.ListeningPorts = append(snap.ListeningPorts, "tcp 0.0.0.0:4444 (nc)")
	snap.SetuidBinaries = append(snap.SetuidBinaries, "/tmp/backdoor")
	out, err = tl.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute 3: %v", err)
	}
	if !strings.Contains(out, "New listening ports (1)") || !strings.Contains(out, "tcp 0.0.0.0:4444 (nc)") {
		t.Errorf("drift output missing new port section: %q", out)
	}
	if !strings.Contains(out, "New setuid binaries (1)") || !strings.Contains(out, "/tmp/backdoor") {
		t.Errorf("drift output missing new setuid section: %q", out)
	}

	// Drift must surface as a warning skillstate so /status flags it even if
	// the model under-reports.
	states, err := skillstate.LoadAll(mem)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	found := false
	for _, st := range states {
		if st.Skill == "check_security_posture" {
			found = true
			if st.Health != skillstate.HealthWarning {
				t.Errorf("skillstate health = %q, want warning after drift", st.Health)
			}
		}
	}
	if !found {
		t.Error("no skillstate recorded for check_security_posture")
	}

	// Snapshots must be persisted to the memory domain for baseline queries.
	var stored check_security_posture.Snapshot
	if err := mem.Domain("check_security_posture").ReadCurrent(&stored); err != nil {
		t.Fatalf("ReadCurrent: %v", err)
	}
	if len(stored.ListeningPorts) != 3 {
		t.Errorf("stored snapshot ports = %d, want 3", len(stored.ListeningPorts))
	}
}
