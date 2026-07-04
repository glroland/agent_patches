package tests

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skills/check_containers"
	"agent_patches/endpoint-server/skillstate"
	"agent_patches/endpoint-server/utils/config"
)

// --- AssessContainer ---

func TestAssessContainer_Dead(t *testing.T) {
	h, issue := check_containers.AssessContainer("svc", "dead", "")
	if h != skillstate.HealthCritical {
		t.Errorf("dead → %q, want critical", h)
	}
	if !strings.Contains(issue, "dead") {
		t.Errorf("issue %q missing 'dead'", issue)
	}
}

func TestAssessContainer_Restarting(t *testing.T) {
	h, issue := check_containers.AssessContainer("svc", "restarting", "Restarting (1) 2 seconds ago")
	if h != skillstate.HealthWarning {
		t.Errorf("restarting → %q, want warning", h)
	}
	if !strings.Contains(issue, "restarting") {
		t.Errorf("issue %q missing 'restarting'", issue)
	}
}

func TestAssessContainer_Paused(t *testing.T) {
	h, _ := check_containers.AssessContainer("svc", "paused", "")
	if h != skillstate.HealthWarning {
		t.Errorf("paused → %q, want warning", h)
	}
}

func TestAssessContainer_ExitedOOM(t *testing.T) {
	h, issue := check_containers.AssessContainer("svc", "exited", "Exited (137) 5 minutes ago")
	if h != skillstate.HealthCritical {
		t.Errorf("exit 137 → %q, want critical", h)
	}
	if !strings.Contains(issue, "OOM") {
		t.Errorf("issue %q missing 'OOM'", issue)
	}
}

func TestAssessContainer_ExitedNonZero(t *testing.T) {
	h, issue := check_containers.AssessContainer("svc", "exited", "Exited (1) 10 minutes ago")
	if h != skillstate.HealthWarning {
		t.Errorf("exit 1 → %q, want warning", h)
	}
	if !strings.Contains(issue, "1") {
		t.Errorf("issue %q missing exit code", issue)
	}
}

func TestAssessContainer_ExitedClean(t *testing.T) {
	h, issue := check_containers.AssessContainer("job", "exited", "Exited (0) 2 hours ago")
	if h != skillstate.HealthOK {
		t.Errorf("exit 0 → %q, want ok", h)
	}
	if issue != "" {
		t.Errorf("exit 0 produced unexpected issue %q", issue)
	}
}

func TestAssessContainer_RunningHealthy(t *testing.T) {
	h, _ := check_containers.AssessContainer("svc", "running", "Up 3 hours (healthy)")
	if h != skillstate.HealthOK {
		t.Errorf("running healthy → %q, want ok", h)
	}
}

func TestAssessContainer_RunningUnhealthy(t *testing.T) {
	h, issue := check_containers.AssessContainer("svc", "running", "Up 2 hours (unhealthy)")
	if h != skillstate.HealthCritical {
		t.Errorf("running unhealthy → %q, want critical", h)
	}
	if !strings.Contains(issue, "health check") {
		t.Errorf("issue %q missing 'health check'", issue)
	}
}

func TestAssessContainer_Created(t *testing.T) {
	h, _ := check_containers.AssessContainer("svc", "created", "Created")
	if h != skillstate.HealthOK {
		t.Errorf("created → %q, want ok", h)
	}
}

// --- WorseHealth ---

func TestWorseHealth_CriticalBeatsWarning(t *testing.T) {
	got := check_containers.WorseHealth(skillstate.HealthWarning, skillstate.HealthCritical)
	if got != skillstate.HealthCritical {
		t.Errorf("WorseHealth(warning, critical) = %q, want critical", got)
	}
}

func TestWorseHealth_WarningBeatsOK(t *testing.T) {
	got := check_containers.WorseHealth(skillstate.HealthOK, skillstate.HealthWarning)
	if got != skillstate.HealthWarning {
		t.Errorf("WorseHealth(ok, warning) = %q, want warning", got)
	}
}

func TestWorseHealth_PreservesHigherLeft(t *testing.T) {
	got := check_containers.WorseHealth(skillstate.HealthCritical, skillstate.HealthOK)
	if got != skillstate.HealthCritical {
		t.Errorf("WorseHealth(critical, ok) = %q, want critical", got)
	}
}

func TestWorseHealth_EqualReturnsEither(t *testing.T) {
	got := check_containers.WorseHealth(skillstate.HealthWarning, skillstate.HealthWarning)
	if got != skillstate.HealthWarning {
		t.Errorf("WorseHealth(warning, warning) = %q, want warning", got)
	}
}

// --- HealthSummary ---

func TestHealthSummary_NoIssues(t *testing.T) {
	got := check_containers.HealthSummary(nil)
	if got != "all containers healthy" {
		t.Errorf("HealthSummary(nil) = %q, want 'all containers healthy'", got)
	}
}

func TestHealthSummary_FewIssues(t *testing.T) {
	issues := []string{"a: dead", "b: restarting"}
	got := check_containers.HealthSummary(issues)
	if !strings.Contains(got, "a: dead") || !strings.Contains(got, "b: restarting") {
		t.Errorf("HealthSummary(%v) = %q, want both issues", issues, got)
	}
}

func TestHealthSummary_TruncatesAfterThree(t *testing.T) {
	issues := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	got := check_containers.HealthSummary(issues)
	if !strings.Contains(got, "+2 more") {
		t.Errorf("HealthSummary with 5 issues = %q, want '+2 more' suffix", got)
	}
	if strings.Contains(got, "delta") || strings.Contains(got, "epsilon") {
		t.Errorf("HealthSummary should not include 4th/5th issue: %q", got)
	}
}

// --- ExitCode ---

func TestExitCode_ParsesCode(t *testing.T) {
	cases := []struct {
		status string
		want   int
	}{
		{"Exited (0) 2 hours ago", 0},
		{"Exited (1) 10 minutes ago", 1},
		{"Exited (137) 5 minutes ago", 137},
		{"Exited (255) 1 second ago", 255},
	}
	for _, c := range cases {
		if got := check_containers.ExitCode(c.status); got != c.want {
			t.Errorf("ExitCode(%q) = %d, want %d", c.status, got, c.want)
		}
	}
}

func TestExitCode_NoMatch(t *testing.T) {
	for _, s := range []string{"", "Up 3 hours", "Restarting (1) 2 seconds ago"} {
		if got := check_containers.ExitCode(s); got != 0 {
			t.Errorf("ExitCode(%q) = %d, want 0", s, got)
		}
	}
}

// --- Tool construction and execution ---

func TestNewCheckContainersTool_NameAndDescription(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tl, err := check_containers.NewCheckContainersTool(mem)
	if err != nil {
		t.Fatalf("NewCheckContainersTool() error: %v", err)
	}
	if got := tl.Name(); got != "check_containers" {
		t.Errorf("Name() = %q, want check_containers", got)
	}
	if tl.Description() == "" {
		t.Error("Description() returned empty string")
	}
}

// TestCheckContainersTool_Execute_NoRuntime verifies the tool returns a
// graceful "no runtime found" message and does not error when neither docker
// nor podman exist on PATH.
func TestCheckContainersTool_Execute_NoRuntime(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty dir — no binaries

	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tl, err := check_containers.NewCheckContainersTool(mem)
	if err != nil {
		t.Fatalf("NewCheckContainersTool() error: %v", err)
	}

	input, _ := json.Marshal(struct{}{})
	result, err := tl.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if !strings.Contains(result, "no container runtime") {
		t.Errorf("Execute() = %q, want 'no container runtime' message", result)
	}
}

// --- AutoResponsibility ---

func TestAutoResponsibility_NoRuntime(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, ok := check_containers.AutoResponsibility()
	if ok {
		t.Error("AutoResponsibility() = true, want false when no runtime on PATH")
	}
}

func TestAutoResponsibility_NoContainers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub approach not used on Windows")
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "docker")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	_, ok := check_containers.AutoResponsibility()
	if ok {
		t.Error("AutoResponsibility() = true, want false when runtime reports no containers")
	}
}

func TestAutoResponsibility_WithRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub approach not used on Windows")
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "docker")
	script := "#!/bin/sh\n" +
		`echo '{"ID":"abc123","Names":"web","Image":"nginx","State":"running","Status":"Up 2 hours"}'` + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	rs, ok := check_containers.AutoResponsibility()
	if !ok {
		t.Fatal("AutoResponsibility() = false, want true when docker stub reports a container")
	}
	if rs.Name != "container-health-check" {
		t.Errorf("Name = %q, want container-health-check", rs.Name)
	}
	if rs.Frequency == "" {
		t.Error("Frequency is empty")
	}
	if len(rs.Tools) == 0 {
		t.Error("Tools list is empty")
	}
	if !slices.Contains(rs.Tools, "check_containers") {
		t.Errorf("Tools %v does not include check_containers", rs.Tools)
	}
}

// TestCheckContainersTool_Execute_WritesSkillState verifies a skillstate entry
// is always saved, even when no runtimes are installed.
func TestCheckContainersTool_Execute_WritesSkillState(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tl, err := check_containers.NewCheckContainersTool(mem)
	if err != nil {
		t.Fatalf("NewCheckContainersTool() error: %v", err)
	}

	input, _ := json.Marshal(struct{}{})
	if _, err := tl.Execute(context.Background(), input); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	states, err := skillstate.LoadAll(mem)
	if err != nil {
		t.Fatalf("skillstate.LoadAll: %v", err)
	}
	if len(states) != 1 || states[0].Skill != "check_containers" {
		t.Fatalf("skillstate = %+v, want one entry for check_containers", states)
	}
	if states[0].Health != skillstate.HealthOK {
		t.Errorf("skillstate health = %q, want ok when no runtime found", states[0].Health)
	}
}
