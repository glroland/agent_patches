package check_security_posture

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"agent_patches/endpoint-server/utils/config"
)

// portEntryRe splits one formatted ListeningPorts entry — e.g.
// "tcp 0.0.0.0:22 (sshd)" or "udp [::]:68" — into its protocol, local
// address:port, and optional process name.
var portEntryRe = regexp.MustCompile(`^(tcp|udp)\s+(\S+)(?:\s+\(([^)]*)\))?$`)

// defaultUnclassifiedSeverity is applied to a listening port that matches no
// baseline_ports.csv row at all: an unrecognized deviation from the OS
// baseline, but not a port this profile specifically knows to be risky.
const defaultUnclassifiedSeverity = "warning"

// severityRank orders findings for display, most severe first.
var severityRank = map[string]int{"critical": 0, "warning": 1, "info": 2}

// portFinding is one listening port classified against the baseline profile.
type portFinding struct {
	Entry       string // original formatted entry, e.g. "tcp 0.0.0.0:4444 (nc)"
	Severity    string // "info", "warning", or "critical"
	Description string
}

// classifyPorts compares the currently listening ports against the loaded
// baseline profile. A port whose baseline row is severity "baseline" is
// expected and omitted from the result. A port matching a risk row (severity
// "info"/"warning"/"critical") is always reported at that severity. A port
// matching no row at all is reported at defaultUnclassifiedSeverity. A
// nil/empty baseline (no baseline_ports.csv deployed) yields no findings —
// the feature is simply inactive.
func classifyPorts(entries []string, baseline []config.BaselinePortEntry) []portFinding {
	if len(baseline) == 0 {
		return nil
	}
	lookup := make(map[string]config.BaselinePortEntry, len(baseline))
	for _, b := range baseline {
		lookup[fmt.Sprintf("%s:%d", b.Protocol, b.Port)] = b
	}

	var findings []portFinding
	for _, entry := range entries {
		m := portEntryRe.FindStringSubmatch(entry)
		if m == nil {
			continue
		}
		proto, addr := m[1], m[2]
		port, ok := portFromAddr(addr)
		if !ok {
			continue
		}
		key := fmt.Sprintf("%s:%d", proto, port)
		if b, found := lookup[key]; found {
			if b.Severity == "baseline" {
				continue
			}
			findings = append(findings, portFinding{Entry: entry, Severity: b.Severity, Description: b.Description})
			continue
		}
		findings = append(findings, portFinding{
			Entry: entry, Severity: defaultUnclassifiedSeverity, Description: "not in baseline profile",
		})
	}
	sort.SliceStable(findings, func(i, j int) bool {
		if severityRank[findings[i].Severity] != severityRank[findings[j].Severity] {
			return severityRank[findings[i].Severity] < severityRank[findings[j].Severity]
		}
		return findings[i].Entry < findings[j].Entry
	})
	return findings
}

// portFromAddr extracts the trailing :port from a formatted local address,
// e.g. "0.0.0.0:22" -> 22, "[::]:22" -> 22, "*:22" -> 22.
func portFromAddr(addr string) (int, bool) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return 0, false
	}
	port, err := strconv.Atoi(addr[i+1:])
	if err != nil {
		return 0, false
	}
	return port, true
}

// hasCriticalFinding reports whether any finding is critical severity.
func hasCriticalFinding(findings []portFinding) bool {
	for _, f := range findings {
		if f.Severity == "critical" {
			return true
		}
	}
	return false
}

// hasWarningFinding reports whether any finding is warning severity.
func hasWarningFinding(findings []portFinding) bool {
	for _, f := range findings {
		if f.Severity == "warning" {
			return true
		}
	}
	return false
}

// writePortFindingsSection renders the "ports beyond baseline" report block.
func writePortFindingsSection(sb *strings.Builder, findings []portFinding) {
	if len(findings) == 0 {
		return
	}
	sb.WriteString("\n=== Ports Beyond Baseline Profile ===\n")
	for i, f := range findings {
		if i == maxListedChanges {
			fmt.Fprintf(sb, "  … and %d more\n", len(findings)-maxListedChanges)
			break
		}
		fmt.Fprintf(sb, "  [%s] %s — %s\n", strings.ToUpper(f.Severity), f.Entry, f.Description)
	}
}

// summarizePortFindings renders a compact "N critical, M warning" style
// count breakdown for the one-line skillstate summary.
func summarizePortFindings(findings []portFinding) string {
	counts := map[string]int{}
	for _, f := range findings {
		counts[f.Severity]++
	}
	var parts []string
	for _, sev := range []string{"critical", "warning", "info"} {
		if counts[sev] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[sev], sev))
		}
	}
	return strings.Join(parts, ", ")
}
