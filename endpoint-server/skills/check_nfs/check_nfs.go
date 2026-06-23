// Package check_nfs monitors NFS mount point health: pending RPC operations,
// GETATTR latency, and D-state processes. On Linux it performs an emergency
// lazy unmount when pending ops exceed the critical threshold so a
// non-responsive NFS server cannot hang the entire host.
package check_nfs

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skillstate"
	"agent_patches/endpoint-server/utils/config"
)

// Health thresholds.
const (
	pendingOpsWarning  int64   = 500
	pendingOpsCritical int64   = 2000
	dStateProcWarning  int     = 1
	dStateProcCritical int     = 5
	getattrMsWarning   float64 = 1000
	getattrMsCritical  float64 = 5000
)

// NFSMountStats holds the sampled health metrics for one NFS mount point.
type NFSMountStats struct {
	Mount       string  // local mount point (e.g., /mnt/nfs)
	Remote      string  // server:export  (e.g., nas:/data)
	PendingOps  int64   // current pending RPC requests (from xprt line)
	GETATTRMs   float64 // average GETATTR round-trip time in ms; 0 if unknown
	DStateProcs int     // number of D-state processes attributed to NFS
}

// RawNFSStats is the parsed output of one mountstats block. Exported for testing.
type RawNFSStats struct {
	PendingOps   int64   // instantaneous pending RPCs from xprt line
	GETATTRMs    float64 // avg GETATTR RTT in ms
	GETATTROps   int64   // total GETATTR ops (context for the latency figure)
}

type nfsCheckInput struct{}

// NewCheckNFSTool returns a skill that surveys all NFS mounts and their health.
func NewCheckNFSTool(mem *memory.Store) (tool.Tool, error) {
	return tool.New(
		"check_nfs",
		"Checks the health of all NFS mount points on this host. Reports pending "+
			"RPC operations, average GETATTR latency, and D-state (uninterruptible "+
			"sleep) processes caused by NFS. Flags mounts with high queue depth or "+
			"slow response times, and records the worst health as a skillstate entry "+
			"so problems appear in GET /status between scheduled runs. On Linux, "+
			"automatically executes a lazy unmount (umount -l) when pending ops "+
			"exceed 2000, preventing a non-responsive NFS server from hanging the host.",
		func(ctx context.Context, _ nfsCheckInput) (string, error) {
			slog.Info("check_nfs: starting")

			mounts, err := listNFSMounts()
			if err != nil {
				slog.Warn("check_nfs: failed to list NFS mounts", "error", err)
			}

			if len(mounts) == 0 {
				msg := "no NFS mounts found on this host"
				_ = skillstate.Save(mem, "check_nfs", skillstate.HealthOK, msg)
				slog.Info("check_nfs: no NFS mounts found")
				return msg, nil
			}

			dStateTotal := countDStateProcs()

			stats, statErr := gatherStats(ctx, mounts)
			if statErr != nil {
				slog.Warn("check_nfs: partial stat failure", "error", statErr)
			}

			// Distribute D-state count evenly across mounts (we can't attribute
			// per-mount without tracing individual syscalls).
			for i := range stats {
				stats[i].DStateProcs = dStateTotal
			}

			var autoUnmounted []string
			var sections []string
			worst := skillstate.HealthOK
			var allIssues []string

			for _, s := range stats {
				h, issue := AssessMount(s)

				if h == skillstate.HealthCritical && s.PendingOps >= pendingOpsCritical {
					if umErr := lazyUnmount(ctx, s.Mount); umErr != nil {
						slog.Error("check_nfs: auto-unmount failed", "mount", s.Mount, "error", umErr)
						issue += fmt.Sprintf(" (auto-unmount failed: %v)", umErr)
					} else {
						slog.Warn("check_nfs: emergency lazy unmount executed", "mount", s.Mount, "pending_ops", s.PendingOps)
						autoUnmounted = append(autoUnmounted, s.Mount)
						issue += " [AUTO-UNMOUNTED]"
					}
				}

				if issue != "" {
					allIssues = append(allIssues, issue)
				}
				worst = worseHealth(worst, h)
				sections = append(sections, buildMountSection(s, h, issue))
			}

			var report strings.Builder
			report.WriteString(strings.Join(sections, "\n\n"))
			if len(autoUnmounted) > 0 {
				fmt.Fprintf(&report, "\n\nEMERGENCY LAZY UNMOUNT executed for: %s", strings.Join(autoUnmounted, ", "))
			}

			summary := nfsSummary(allIssues, len(mounts))
			_ = skillstate.Save(mem, "check_nfs", worst, summary)
			slog.Info("check_nfs: completed", "mounts", len(mounts), "health", worst, "issues", len(allIssues))
			return report.String(), nil
		},
	)
}

// AssessMount derives a skillstate health level and short issue label for a
// single NFS mount. Exported for testing.
func AssessMount(s NFSMountStats) (skillstate.Health, string) {
	var issues []string
	worst := skillstate.HealthOK

	if s.PendingOps >= pendingOpsCritical {
		worst = skillstate.HealthCritical
		issues = append(issues, fmt.Sprintf("%d pending ops (critical)", s.PendingOps))
	} else if s.PendingOps >= pendingOpsWarning {
		worst = worseHealth(worst, skillstate.HealthWarning)
		issues = append(issues, fmt.Sprintf("%d pending ops (warning)", s.PendingOps))
	}

	if s.GETATTRMs >= getattrMsCritical {
		worst = skillstate.HealthCritical
		issues = append(issues, fmt.Sprintf("GETATTR %.0fms (critical)", s.GETATTRMs))
	} else if s.GETATTRMs >= getattrMsWarning {
		worst = worseHealth(worst, skillstate.HealthWarning)
		issues = append(issues, fmt.Sprintf("GETATTR %.0fms (warning)", s.GETATTRMs))
	}

	if s.DStateProcs >= dStateProcCritical {
		worst = skillstate.HealthCritical
		issues = append(issues, fmt.Sprintf("%d D-state NFS processes (critical)", s.DStateProcs))
	} else if s.DStateProcs >= dStateProcWarning {
		worst = worseHealth(worst, skillstate.HealthWarning)
		issues = append(issues, fmt.Sprintf("%d D-state NFS process(es) (warning)", s.DStateProcs))
	}

	if len(issues) == 0 {
		return skillstate.HealthOK, ""
	}
	return worst, fmt.Sprintf("%s: %s", s.Mount, strings.Join(issues, ", "))
}

// ParseMountstats parses the text of /proc/self/mountstats and returns the
// RPC stats for each NFS mount, keyed by local mount point. Exported for testing.
func ParseMountstats(data string) map[string]RawNFSStats {
	result := make(map[string]RawNFSStats)
	var currentMount string
	var inNFS bool
	var current RawNFSStats

	flush := func() {
		if currentMount != "" && inNFS {
			result[currentMount] = current
		}
	}

	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// New device block.
		if strings.HasPrefix(trimmed, "device ") {
			flush()
			currentMount = ""
			inNFS = false
			current = RawNFSStats{}

			// "device <remote> mounted on <mount> with fstype <type> ..."
			parts := strings.Fields(trimmed)
			// Find "mounted", "on", and "with" keywords.
			for i := 0; i+4 < len(parts); i++ {
				if parts[i] == "mounted" && parts[i+1] == "on" && parts[i+3] == "with" {
					currentMount = parts[i+2]
					fstype := ""
					for j := i+4; j < len(parts); j++ {
						if parts[j-1] == "fstype" {
							fstype = parts[j]
							break
						}
					}
					inNFS = strings.HasPrefix(fstype, "nfs")
					break
				}
			}
			continue
		}

		if !inNFS || currentMount == "" {
			continue
		}

		// xprt line: "xprt: tcp <fields...>"
		if strings.HasPrefix(trimmed, "xprt:") {
			fields := strings.Fields(trimmed)
			// fields[0]="xprt:", fields[1]=proto, fields[2..] = values
			if len(fields) >= 3 {
				proto := fields[1]
				vals := fields[2:]
				current.PendingOps = parsePendingOps(proto, vals)
			}
			continue
		}

		// Per-op GETATTR line: "GETATTR: ops sends recvs kB_sent kB_recv cum_queue_ms cum_rtt_ms cum_exe_ms"
		if strings.HasPrefix(trimmed, "GETATTR:") {
			fields := strings.Fields(trimmed)
			// fields[0]="GETATTR:", fields[1]=ops, ..., fields[7]=cum_rtt_ms
			if len(fields) >= 8 {
				ops, _ := strconv.ParseInt(fields[1], 10, 64)
				cumRTT, _ := strconv.ParseFloat(fields[7], 64)
				if ops > 0 {
					current.GETATTROps = ops
					current.GETATTRMs = math.Round(cumRTT/float64(ops)*10) / 10
				}
			}
			continue
		}
	}
	flush()
	return result
}

// parsePendingOps extracts the current pending-ops count from the xprt
// field slice. The field layout differs by protocol and kernel version;
// the pending_u field is the last meaningful value on the xprt line.
func parsePendingOps(proto string, vals []string) int64 {
	// TCP (modern kernels, 13 values after protocol):
	//   port bind connect connect_to idle sends recvs bad_xid req_u backlog_u max_slots sending_u pending_u
	// pending_u is index 12 (last).
	// UDP (7 values): port bind sends recvs bad_xid req_u backlog_u
	// pending_u not applicable for UDP.
	switch proto {
	case "tcp", "tcp6":
		if len(vals) >= 13 {
			n, _ := strconv.ParseInt(vals[12], 10, 64)
			return n
		}
		// Older kernel — use req_u (index 8) as a proxy; it's cumulative but
		// non-zero values still indicate queue activity.
		if len(vals) >= 9 {
			n, _ := strconv.ParseInt(vals[8], 10, 64)
			return n
		}
	}
	return 0
}

// worseHealth returns the more severe of a and b.
func worseHealth(a, b skillstate.Health) skillstate.Health {
	if severityOf(b) > severityOf(a) {
		return b
	}
	return a
}

func severityOf(h skillstate.Health) int {
	switch h {
	case skillstate.HealthWarning:
		return 1
	case skillstate.HealthCritical:
		return 2
	default:
		return 0
	}
}

// nfsSummary produces the skillstate summary string.
func nfsSummary(issues []string, totalMounts int) string {
	if len(issues) == 0 {
		return fmt.Sprintf("all %d NFS mount(s) healthy", totalMounts)
	}
	top := issues
	suffix := ""
	if len(issues) > 3 {
		top = issues[:3]
		suffix = fmt.Sprintf(" (+%d more)", len(issues)-3)
	}
	return strings.Join(top, "; ") + suffix
}

// buildMountSection composes a human-readable report block for one mount.
func buildMountSection(s NFSMountStats, h skillstate.Health, issue string) string {
	marker := "  "
	switch h {
	case skillstate.HealthCritical:
		marker = "!!"
	case skillstate.HealthWarning:
		marker = " !"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s Mount:       %s\n", marker, s.Mount)
	if s.Remote != "" {
		fmt.Fprintf(&sb, "   Remote:      %s\n", s.Remote)
	}
	fmt.Fprintf(&sb, "   Pending ops: %d\n", s.PendingOps)
	if s.GETATTRMs > 0 {
		fmt.Fprintf(&sb, "   GETATTR avg: %.1f ms\n", s.GETATTRMs)
	}
	fmt.Fprintf(&sb, "   D-state:     %d process(es)\n", s.DStateProcs)
	if issue != "" {
		fmt.Fprintf(&sb, "   Issue:       %s\n", issue)
	}
	return sb.String()
}

// AutoResponsibility returns the built-in nfs-health-check responsibility and
// true on Linux (where full monitoring is supported). Returns false on other
// platforms; callers should skip injection.
func AutoResponsibility() (config.ResponsibilitySettings, bool) {
	if !nfsSupported() {
		return config.ResponsibilitySettings{}, false
	}
	return config.ResponsibilitySettings{
		Name:      "nfs-health-check",
		Frequency: "5m",
		Instruction: `Check the health of all NFS mount points on this host.
Review pending RPC operations, GETATTR latency, and D-state processes.
If any mount has elevated pending ops (>500) or slow GETATTR latency (>1000ms),
use run_diagnostic_command to investigate further — for example:
  nfsstat -m                     # per-mount NFS statistics
  dmesg | grep -i nfs | tail -20 # kernel NFS messages
  ps aux | awk '$8 ~ /^D/ {print}' | head -20  # D-state processes
Report your findings. If the skill has already performed an emergency unmount
(shown as [AUTO-UNMOUNTED] in the output), report that action via report_findings
and investigate why the NFS server became unresponsive. Do not re-mount without
operator approval via run_approved_command.`,
		Tools:        []string{"check_nfs", "run_diagnostic_command", "run_approved_command", "report_findings"},
		WhenToNotify: "on error",
	}, true
}
