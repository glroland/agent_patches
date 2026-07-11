// Package check_containers lists Docker and Podman containers and assesses
// their health, recording a skillstate entry so problems appear in GET /status
// even between scheduled responsibility runs.
package check_containers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skillstate"
	"agent_patches/endpoint-server/utils/config"
)

// psEntry holds the subset of fields returned by `docker/podman ps --format json`.
type psEntry struct {
	ID     string `json:"ID"`
	Names  string `json:"Names"`
	Image  string `json:"Image"`
	State  string `json:"State"`
	Status string `json:"Status"`
	Ports  string `json:"Ports"`
}

// exitCodeRe extracts the exit code from a status string like "Exited (1) 3 minutes ago".
var exitCodeRe = regexp.MustCompile(`Exited \((\d+)\)`)

type checkContainersInput struct{}

// CheckResult is the outcome of one container health survey.
type CheckResult struct {
	Health  skillstate.Health
	Report  string
	Summary string
}

// runCheck surveys all Docker/Podman containers, records the worst health as
// a skillstate entry, and returns a human-readable report. It makes no LLM
// calls — callers decide whether the result warrants one.
func runCheck(ctx context.Context, mem *memory.Store) (CheckResult, error) {
	slog.Info("check_containers: starting")

	var sections []string
	var allIssues []string
	worst := skillstate.HealthOK

	for _, runtime := range []string{"docker", "podman"} {
		if _, err := exec.LookPath(runtime); err != nil {
			continue
		}
		slog.Debug("check_containers: scanning runtime", "runtime", runtime)

		containers, err := listContainers(ctx, runtime)
		if err != nil {
			slog.Warn("check_containers: list failed", "runtime", runtime, "error", err)
			issue := fmt.Sprintf("%s: failed to list containers (%v)", runtime, err)
			allIssues = append(allIssues, issue)
			worst = WorseHealth(worst, skillstate.HealthWarning)
			sections = append(sections, fmt.Sprintf("=== %s ===\n%s", runtime, issue))
			continue
		}
		if len(containers) == 0 {
			sections = append(sections, fmt.Sprintf("=== %s ===\nNo containers found.", runtime))
			continue
		}

		section, issues, health := buildSection(runtime, containers)
		sections = append(sections, section)
		allIssues = append(allIssues, issues...)
		worst = WorseHealth(worst, health)
	}

	if len(sections) == 0 {
		msg := "no container runtime (docker/podman) found on this host"
		_ = skillstate.Save(mem, "check_containers", skillstate.HealthOK, msg)
		slog.Info("check_containers: no runtime found")
		return CheckResult{Health: skillstate.HealthOK, Report: msg, Summary: msg}, nil
	}

	report := strings.Join(sections, "\n\n")
	summary := HealthSummary(allIssues)
	_ = skillstate.Save(mem, "check_containers", worst, summary)
	slog.Info("check_containers: completed", "health", worst, "issues", len(allIssues))
	return CheckResult{Health: worst, Report: report, Summary: summary}, nil
}

// NewCheckContainersTool returns a skill that lists all containers across
// Docker and Podman runtimes and assesses their health state.
func NewCheckContainersTool(mem *memory.Store) (tool.Tool, error) {
	return tool.New(
		"check_containers",
		"Lists all Docker and Podman containers on this host and reports their "+
			"current state and health status. Flags containers that are stopped "+
			"unexpectedly, stuck in a restart loop, killed by OOM, or marked "+
			"unhealthy by their own health check. Records the result as the "+
			"skill's last known health state so problems appear in GET /status "+
			"automatically between scheduled runs.",
		func(ctx context.Context, _ checkContainersInput) (string, error) {
			res, err := runCheck(ctx, mem)
			return res.Report, err
		},
	)
}

// NewPreCheck returns a loop.PreCheck-compatible function that runs the
// container health survey directly, bypassing the LLM tool-use loop entirely.
// It reports needsLLM=false whenever every container is healthy — the common
// case on most scheduled ticks — so the loop can skip the LLM call outright
// instead of invoking it just to have it discover nothing is wrong.
func NewPreCheck(mem *memory.Store) func(ctx context.Context) (bool, string, error) {
	return func(ctx context.Context) (bool, string, error) {
		res, err := runCheck(ctx, mem)
		if err != nil {
			// Fail open: let the LLM path see and report the failure rather
			// than silently going quiet on a broken health check.
			return true, "", err
		}
		return res.Health != skillstate.HealthOK, res.Report, nil
	}
}

// listContainers runs `<runtime> ps -a` and returns parsed entries.
func listContainers(ctx context.Context, runtime string) ([]psEntry, error) {
	// --format '{{json .}}' emits one JSON object per line (NDJSON) and works
	// on both older Docker and Podman; --format json (array) requires newer Docker.
	out, err := exec.CommandContext(ctx, runtime, "ps", "-a", "--no-trunc",
		"--format", "{{json .}}").Output()
	if err != nil {
		return nil, err
	}

	var entries []psEntry
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e psEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			slog.Debug("check_containers: skipping unparseable line", "line", line, "error", err)
			continue
		}
		entries = append(entries, e)
	}
	return entries, sc.Err()
}

// buildSection formats a human-readable section for one runtime and returns
// the issues found and worst health level observed.
func buildSection(runtime string, containers []psEntry) (section string, issues []string, health skillstate.Health) {
	health = skillstate.HealthOK
	var sb strings.Builder
	fmt.Fprintf(&sb, "=== %s (%d container(s)) ===\n", runtime, len(containers))

	for _, c := range containers {
		name := c.Names
		if name == "" {
			name = c.ID[:min(12, len(c.ID))]
		}
		state := strings.ToLower(c.State)
		status := c.Status

		// Determine per-container health and any issue label.
		h, issue := assessContainer(name, state, status)
		health = WorseHealth(health, h)

		marker := "  "
		switch h {
		case skillstate.HealthCritical:
			marker = "!!"
			issues = append(issues, issue)
		case skillstate.HealthWarning:
			marker = " !"
			issues = append(issues, issue)
		}

		fmt.Fprintf(&sb, "%s %-30s  %-12s  %s", marker, name, state, status)
		if c.Ports != "" {
			fmt.Fprintf(&sb, "  [%s]", c.Ports)
		}
		if issue != "" {
			fmt.Fprintf(&sb, "  <- %s", issue)
		}
		fmt.Fprintln(&sb)
	}

	return sb.String(), issues, health
}

// assessContainer returns the health level and a short issue description for
// a single container.
func assessContainer(name, state, status string) (skillstate.Health, string) {
	switch state {
	case "dead":
		return skillstate.HealthCritical, fmt.Sprintf("%s: dead", name)

	case "restarting":
		return skillstate.HealthWarning, fmt.Sprintf("%s: restarting (possible restart loop)", name)

	case "paused":
		return skillstate.HealthWarning, fmt.Sprintf("%s: paused", name)

	case "exited":
		code := ExitCode(status)
		switch {
		case code == 137:
			// SIGKILL / OOM kill
			return skillstate.HealthCritical, fmt.Sprintf("%s: OOM killed (exit 137)", name)
		case code != 0:
			return skillstate.HealthWarning, fmt.Sprintf("%s: exited with code %d", name, code)
		}
		// Clean exit (code 0) — not a problem for one-shot containers.
		return skillstate.HealthOK, ""

	case "running":
		if strings.Contains(status, "(unhealthy)") {
			return skillstate.HealthCritical, fmt.Sprintf("%s: health check failing", name)
		}
		return skillstate.HealthOK, ""

	case "created":
		// Never started — flag as informational but not an error.
		return skillstate.HealthOK, ""
	}

	return skillstate.HealthOK, ""
}

// ExitCode extracts the numeric exit code from a status string such as
// "Exited (1) 3 minutes ago". Returns 0 if no exit code is found.
func ExitCode(status string) int {
	m := exitCodeRe.FindStringSubmatch(status)
	if len(m) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// WorseHealth returns whichever of a and b is more severe.
func WorseHealth(a, b skillstate.Health) skillstate.Health {
	severity := map[skillstate.Health]int{
		skillstate.HealthOK:       0,
		skillstate.HealthWarning:  1,
		skillstate.HealthCritical: 2,
	}
	if severity[b] > severity[a] {
		return b
	}
	return a
}

// HealthSummary produces a short skillstate summary string from the collected issues.
func HealthSummary(issues []string) string {
	if len(issues) == 0 {
		return "all containers healthy"
	}
	top := issues[:min(3, len(issues))]
	suffix := ""
	if len(issues) > 3 {
		suffix = fmt.Sprintf(" (+%d more)", len(issues)-3)
	}
	return strings.Join(top, "; ") + suffix
}

// AssessContainer returns the health level and a short issue description for
// a single container. Exported for testing.
func AssessContainer(name, state, status string) (skillstate.Health, string) {
	return assessContainer(name, state, status)
}

// AutoResponsibility returns the built-in container-health-check responsibility
// and true if docker or podman is found on PATH and at least one container
// (running or stopped) exists. Returns false when neither runtime is installed
// or both report no containers; callers should skip injection.
func AutoResponsibility() (config.ResponsibilitySettings, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	found := false
	for _, rt := range []string{"docker", "podman"} {
		if _, err := exec.LookPath(rt); err != nil {
			continue
		}
		containers, err := listContainers(ctx, rt)
		if err != nil {
			slog.Warn("check_containers: failed to list containers for auto-responsibility",
				"runtime", rt, "error", err)
			continue
		}
		if len(containers) > 0 {
			found = true
			break
		}
	}
	if !found {
		return config.ResponsibilitySettings{}, false
	}
	return config.ResponsibilitySettings{
		Name:      "container-health-check",
		Frequency: "15m",
		Instruction: `A container health issue was detected on this host (see the
pre-gathered health report included with this instruction — no need to call
check_containers again unless you want a fresh read). Containers stopped
unexpectedly, stuck in a restart loop, OOM killed (exit 137), or failing their
health check all warrant investigation. Use run_diagnostic_command to
investigate logs (e.g. docker logs --tail 50 <name> or
podman logs --tail 50 <name>) before proposing any restart via
run_approved_command. Do not flag containers that exited cleanly
(exit code 0) — those are expected for one-shot jobs. Call report_findings
with your assessment.`,
		Tools:        []string{"check_containers", "run_diagnostic_command", "run_approved_command", "report_findings"},
		WhenToNotify: "on error",
	}, true
}
