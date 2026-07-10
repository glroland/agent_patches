// Package check_security_posture provides a skill that captures a security
// posture snapshot of the host — listening ports, local users, admin group
// membership, sudoers fingerprint, per-user authorized_keys fingerprints, and
// setuid binaries — and reports what CHANGED since the previous snapshot.
//
// A sysadmin reviewing security cares about drift, not state: a new listening
// port, a new admin user, a modified sudoers file, or a fresh setuid binary
// is signal; the steady-state list is noise. Snapshots are stored in the
// tiered memory domain "check_security_posture", so the agent can also
// compare against ~24h/~7d baselines via compare_to_baseline.
package check_security_posture

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skillstate"
	"agent_patches/endpoint-server/utils/config"
)

// domainName is the memory domain snapshots are written to.
const domainName = "check_security_posture"

// maxListedChanges caps how many items of each change class are enumerated in
// the report text.
const maxListedChanges = 40

// Snapshot is one point-in-time capture of the host's security posture.
// List fields are sorted so diffs and stored JSON are stable.
type Snapshot struct {
	// ListeningPorts holds one formatted entry per listening socket,
	// e.g. "tcp 0.0.0.0:22 (sshd)".
	ListeningPorts []string `json:"listening_ports"`
	// Users are login-capable local accounts, e.g. "lee (uid 1000)".
	Users []string `json:"users"`
	// AdminUsers are members of the admin group (sudo/wheel on Linux,
	// Administrators on Windows) plus uid-0 accounts.
	AdminUsers []string `json:"admin_users"`
	// SudoersHash fingerprints /etc/sudoers and /etc/sudoers.d (content
	// sha256 when readable, mtime+size otherwise). Empty on Windows.
	SudoersHash string `json:"sudoers_hash,omitempty"`
	// AuthorizedKeys maps user -> fingerprint of their authorized_keys file.
	AuthorizedKeys map[string]string `json:"authorized_keys,omitempty"`
	// SetuidBinaries are setuid/setgid executables in standard binary dirs.
	SetuidBinaries []string `json:"setuid_binaries,omitempty"`
	// Errors lists non-fatal gathering problems (e.g. a command not found),
	// so a partial snapshot is still comparable and honest about its gaps.
	Errors []string `json:"errors,omitempty"`
}

// Diff describes what changed between two snapshots.
type Diff struct {
	AddedPorts, RemovedPorts   []string
	AddedUsers, RemovedUsers   []string
	AddedAdmins, RemovedAdmins []string
	SudoersChanged             bool
	ChangedAuthorizedKeys      []string // users whose fingerprint appeared, disappeared, or changed
	AddedSetuid, RemovedSetuid []string
}

// Empty reports whether the diff contains no changes.
func (d Diff) Empty() bool {
	return len(d.AddedPorts) == 0 && len(d.RemovedPorts) == 0 &&
		len(d.AddedUsers) == 0 && len(d.RemovedUsers) == 0 &&
		len(d.AddedAdmins) == 0 && len(d.RemovedAdmins) == 0 &&
		!d.SudoersChanged && len(d.ChangedAuthorizedKeys) == 0 &&
		len(d.AddedSetuid) == 0 && len(d.RemovedSetuid) == 0
}

// DiffSnapshots computes what changed from prev to cur.
func DiffSnapshots(prev, cur Snapshot) Diff {
	d := Diff{
		SudoersChanged: prev.SudoersHash != cur.SudoersHash,
	}
	d.AddedPorts, d.RemovedPorts = diffLists(prev.ListeningPorts, cur.ListeningPorts)
	d.AddedUsers, d.RemovedUsers = diffLists(prev.Users, cur.Users)
	d.AddedAdmins, d.RemovedAdmins = diffLists(prev.AdminUsers, cur.AdminUsers)
	d.AddedSetuid, d.RemovedSetuid = diffLists(prev.SetuidBinaries, cur.SetuidBinaries)

	users := make(map[string]bool)
	for u := range prev.AuthorizedKeys {
		users[u] = true
	}
	for u := range cur.AuthorizedKeys {
		users[u] = true
	}
	for u := range users {
		if prev.AuthorizedKeys[u] != cur.AuthorizedKeys[u] {
			d.ChangedAuthorizedKeys = append(d.ChangedAuthorizedKeys, u)
		}
	}
	sort.Strings(d.ChangedAuthorizedKeys)
	return d
}

// diffLists returns the entries added to and removed from prev, given both
// lists' membership (order-insensitive).
func diffLists(prev, cur []string) (added, removed []string) {
	prevSet := make(map[string]bool, len(prev))
	for _, s := range prev {
		prevSet[s] = true
	}
	curSet := make(map[string]bool, len(cur))
	for _, s := range cur {
		curSet[s] = true
	}
	for _, s := range cur {
		if !prevSet[s] {
			added = append(added, s)
		}
	}
	for _, s := range prev {
		if !curSet[s] {
			removed = append(removed, s)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

// NewCheckSecurityPostureTool returns the production tool using the
// platform-specific gatherer.
func NewCheckSecurityPostureTool(mem *memory.Store, baseline []config.BaselinePortEntry) (tool.Tool, error) {
	return NewWithGatherer(mem, gather, baseline)
}

// NewWithGatherer returns the tool with an injected snapshot gatherer.
// Exported for tests. baseline may be nil, in which case port classification
// against a baseline profile is skipped entirely.
func NewWithGatherer(mem *memory.Store, gatherFn func(ctx context.Context) Snapshot, baseline []config.BaselinePortEntry) (tool.Tool, error) {
	return tool.New(
		"check_security_posture",
		"Capture the host's security posture — listening ports (with owning process), "+
			"login-capable local users, admin/sudo group membership, a sudoers fingerprint, "+
			"per-user authorized_keys fingerprints, and setuid binaries — and compare it "+
			"against the previous snapshot. The report leads with what CHANGED (new/removed "+
			"ports, users, admins, setuid binaries; sudoers or authorized_keys modifications) "+
			"followed by the current posture. When a baseline_ports.csv profile is deployed, "+
			"the report also lists listening ports that fall outside that OS-specific baseline "+
			"(and any known-risk port regardless of baseline status), each tagged info/warning/"+
			"critical. That tag is a starting point, not a verdict — weigh the host's stated "+
			"system purpose before treating a flagged port as an incident. Snapshots are stored "+
			"in the check_security_posture memory domain, so compare_to_baseline can also "+
			"provide ~24h/~7d comparisons. Changes are facts, not verdicts — investigate "+
			"unexpected ones with run_diagnostic_command before judging severity.",
		func(ctx context.Context, _ struct{}) (string, error) {
			slog.Info("check_security_posture: starting")

			cur := gatherFn(ctx)

			d := mem.Domain(domainName)
			var prev Snapshot
			hasPrev := d.ReadCurrent(&prev) == nil

			if err := d.Write(cur); err != nil {
				slog.Warn("check_security_posture: failed to store snapshot", "error", err)
				cur.Errors = append(cur.Errors, fmt.Sprintf("snapshot not persisted: %v", err))
			}

			portFindings := classifyPorts(cur.ListeningPorts, baseline)

			var sb strings.Builder
			var diff Diff
			if hasPrev {
				diff = DiffSnapshots(prev, cur)
				writeDiffSection(&sb, diff)
			} else {
				sb.WriteString("=== Changes Since Last Check ===\n")
				sb.WriteString("No previous snapshot — this run establishes the baseline.\n")
			}
			writeCurrentSection(&sb, cur)
			writePortFindingsSection(&sb, portFindings)

			summary := stateSummary(cur, diff, hasPrev, portFindings)
			health := skillstate.HealthOK
			if hasPrev && !diff.Empty() {
				health = skillstate.HealthWarning
			}
			if hasWarningFinding(portFindings) {
				health = skillstate.HealthWarning
			}
			if hasCriticalFinding(portFindings) {
				health = skillstate.HealthCritical
			}
			_ = skillstate.Save(mem, "check_security_posture", health, summary)

			slog.Info("check_security_posture: completed",
				"ports", len(cur.ListeningPorts), "users", len(cur.Users),
				"admins", len(cur.AdminUsers), "setuid", len(cur.SetuidBinaries),
				"changed", hasPrev && !diff.Empty(), "gather_errors", len(cur.Errors),
				"ports_beyond_baseline", len(portFindings))
			return sb.String(), nil
		},
	)
}

// writeDiffSection renders the changes block of the report.
func writeDiffSection(sb *strings.Builder, d Diff) {
	sb.WriteString("=== Changes Since Last Check ===\n")
	if d.Empty() {
		sb.WriteString("No changes detected.\n")
		return
	}
	writeChangeList(sb, "New listening ports", d.AddedPorts)
	writeChangeList(sb, "Removed listening ports", d.RemovedPorts)
	writeChangeList(sb, "New users", d.AddedUsers)
	writeChangeList(sb, "Removed users", d.RemovedUsers)
	writeChangeList(sb, "New admin users", d.AddedAdmins)
	writeChangeList(sb, "Removed admin users", d.RemovedAdmins)
	if d.SudoersChanged {
		sb.WriteString("Sudoers configuration CHANGED (fingerprint differs from last check)\n")
	}
	writeChangeList(sb, "authorized_keys changed for", d.ChangedAuthorizedKeys)
	writeChangeList(sb, "New setuid binaries", d.AddedSetuid)
	writeChangeList(sb, "Removed setuid binaries", d.RemovedSetuid)
}

func writeChangeList(sb *strings.Builder, label string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(sb, "%s (%d):\n", label, len(items))
	for i, it := range items {
		if i == maxListedChanges {
			fmt.Fprintf(sb, "  … and %d more\n", len(items)-maxListedChanges)
			break
		}
		fmt.Fprintf(sb, "  %s\n", it)
	}
}

// writeCurrentSection renders the current posture block of the report.
func writeCurrentSection(sb *strings.Builder, s Snapshot) {
	sb.WriteString("\n=== Current Posture ===\n")

	fmt.Fprintf(sb, "Listening ports (%d):\n", len(s.ListeningPorts))
	for i, p := range s.ListeningPorts {
		if i == maxListedChanges {
			fmt.Fprintf(sb, "  … and %d more\n", len(s.ListeningPorts)-maxListedChanges)
			break
		}
		fmt.Fprintf(sb, "  %s\n", p)
	}

	fmt.Fprintf(sb, "Login-capable users (%d): %s\n", len(s.Users), strings.Join(s.Users, ", "))
	fmt.Fprintf(sb, "Admin users (%d): %s\n", len(s.AdminUsers), strings.Join(s.AdminUsers, ", "))
	if s.SudoersHash != "" {
		fmt.Fprintf(sb, "Sudoers fingerprint: %s\n", s.SudoersHash)
	}
	if len(s.AuthorizedKeys) > 0 {
		users := make([]string, 0, len(s.AuthorizedKeys))
		for u := range s.AuthorizedKeys {
			users = append(users, u)
		}
		sort.Strings(users)
		fmt.Fprintf(sb, "Users with authorized_keys (%d): %s\n", len(users), strings.Join(users, ", "))
	}
	fmt.Fprintf(sb, "Setuid/setgid binaries (%d)\n", len(s.SetuidBinaries))

	if len(s.Errors) > 0 {
		sb.WriteString("\nGathering limitations (snapshot is partial):\n")
		for _, e := range s.Errors {
			fmt.Fprintf(sb, "  %s\n", e)
		}
	}
}

// stateSummary builds the one-line skillstate summary.
func stateSummary(s Snapshot, d Diff, hasPrev bool, findings []portFinding) string {
	base := fmt.Sprintf("%d listening ports, %d users, %d admins, %d setuid binaries",
		len(s.ListeningPorts), len(s.Users), len(s.AdminUsers), len(s.SetuidBinaries))
	switch {
	case !hasPrev:
		base += "; baseline established"
	case d.Empty():
		base += "; no changes since last check"
	default:
		changes := len(d.AddedPorts) + len(d.RemovedPorts) + len(d.AddedUsers) + len(d.RemovedUsers) +
			len(d.AddedAdmins) + len(d.RemovedAdmins) + len(d.ChangedAuthorizedKeys) +
			len(d.AddedSetuid) + len(d.RemovedSetuid)
		if d.SudoersChanged {
			changes++
		}
		base += fmt.Sprintf("; %d posture change(s) since last check: %s", changes, summarizeChanges(d))
	}
	if n := len(findings); n > 0 {
		base += fmt.Sprintf("; %d port(s) beyond baseline profile: %s", n, summarizePortFindings(findings))
	}
	return base
}

// maxSummaryItems caps how many items per change category are named in the
// one-line skillstate summary before falling back to "+N more".
const maxSummaryItems = 10

// summarizeChanges renders a compact, single-line description of every
// change category present in d, for use in the skillstate summary.
func summarizeChanges(d Diff) string {
	var parts []string
	addPart := func(label string, items []string) {
		if len(items) == 0 {
			return
		}
		display := items
		suffix := ""
		if len(items) > maxSummaryItems {
			display = items[:maxSummaryItems]
			suffix = fmt.Sprintf(", +%d more", len(items)-maxSummaryItems)
		}
		parts = append(parts, fmt.Sprintf("%s: %s%s", label, strings.Join(display, ", "), suffix))
	}
	addPart("new ports", d.AddedPorts)
	addPart("removed ports", d.RemovedPorts)
	addPart("new users", d.AddedUsers)
	addPart("removed users", d.RemovedUsers)
	addPart("new admins", d.AddedAdmins)
	addPart("removed admins", d.RemovedAdmins)
	if d.SudoersChanged {
		parts = append(parts, "sudoers changed")
	}
	addPart("authorized_keys changed for", d.ChangedAuthorizedKeys)
	addPart("new setuid", d.AddedSetuid)
	addPart("removed setuid", d.RemovedSetuid)
	return strings.Join(parts, "; ")
}
