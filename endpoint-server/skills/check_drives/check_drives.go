package check_drives

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skillstate"
)

// Disk usage thresholds (percent used) for the skill's last-known-state
// health, matching the "above 90% capacity" guidance in the disk-space-check
// responsibility prompt.
const (
	usedPctWarning  = 80.0
	usedPctCritical = 90.0
)

// DiskStat holds usage statistics for one local disk.
type DiskStat struct {
	Mount  string
	Device string
	Total  uint64
	Free   uint64
	FSType string
}

func (d DiskStat) Used() uint64 {
	if d.Free > d.Total {
		return 0
	}
	return d.Total - d.Free
}

func (d DiskStat) UsedPct() float64 {
	if d.Total == 0 {
		return 0
	}
	return float64(d.Used()) / float64(d.Total) * 100
}

type diskUsageInput struct{}

// NewDiskUsageTool returns a task tool that reports current disk space usage
// for all local disks on the host. The result is also recorded as the
// skill's last known state (see skillstate), so a near-full or failing disk
// is reflected in GET /status even if the agent never calls report_findings.
func NewDiskUsageTool(mem *memory.Store) (tool.Tool, error) {
	return tool.New(
		"check_drives",
		"Reports current disk space usage for all local disks on the host, "+
			"including total, used, and free space per mount point, the top "+
			"largest directories and files on each disk, and S.M.A.R.T. health "+
			"status for the underlying physical disk (when available).",
		func(_ context.Context, _ diskUsageInput) (string, error) {
			slog.Info("check_drives: starting")
			disks, err := localDisks()
			if err != nil {
				slog.Info("check_drives: failed", "error", err)
				_ = skillstate.Save(mem, "check_drives", skillstate.HealthCritical, fmt.Sprintf("failed to read disk usage: %v", err))
				return "", fmt.Errorf("disk_usage: %w", err)
			}
			disks = DedupeDisks(disks)
			slog.Debug("check_drives: found local disks", "count", len(disks))
			if len(disks) == 0 {
				slog.Info("check_drives: completed", "disks", 0)
				_ = skillstate.Save(mem, "check_drives", skillstate.HealthOK, "no local disks found")
				return "No local disks found.", nil
			}
			smartCache := collectSmartReports(disks)
			report := buildReport(disks, smartCache)
			slog.Info("check_drives: completed", "disks", len(disks), "output_len", len(report))
			health, summary := diskHealth(disks, smartCache)
			_ = skillstate.Save(mem, "check_drives", health, summary)
			return report, nil
		},
	)
}

// diskHealth derives a skillstate health/summary pair from a set of disks:
// critical if any disk's SMART status has failed or it is at/above
// usedPctCritical, warning if any disk is at/above usedPctWarning, else ok.
func diskHealth(disks []DiskStat, smartCache map[string][]SmartReport) (skillstate.Health, string) {
	var warnings, criticals []string
	for _, d := range disks {
		pct := d.UsedPct()
		switch {
		case pct >= usedPctCritical:
			criticals = append(criticals, fmt.Sprintf("%s is %.1f%% full", d.Mount, pct))
		case pct >= usedPctWarning:
			warnings = append(warnings, fmt.Sprintf("%s is %.1f%% full", d.Mount, pct))
		}
		for _, sr := range smartCache[d.Device] {
			if sr.Available && !sr.Healthy {
				criticals = append(criticals, fmt.Sprintf("SMART status FAILED for %s (%s)", sr.Device, strings.Join(sr.Findings, "; ")))
			}
		}
	}

	switch {
	case len(criticals) > 0:
		return skillstate.HealthCritical, strings.Join(criticals, "; ")
	case len(warnings) > 0:
		return skillstate.HealthWarning, strings.Join(warnings, "; ")
	default:
		return skillstate.HealthOK, "all disks healthy"
	}
}

// dedupeFreeTolerance is how close two mounts' free space must be, relative
// to their (equal) total capacity, to be considered the same underlying
// storage. Filesystems that share space at a level below the mount (e.g.
// APFS containers on macOS, or btrfs/LVM pools on Linux) report slightly
// different "free" values per mount due to per-volume accounting, so an
// exact match is too strict.
const dedupeFreeTolerance = 0.01 // 1% of total capacity

// DedupeDisks drops pseudo-filesystems and collapses multiple mounts that
// report the same underlying storage (identical total capacity and free
// space within dedupeFreeTolerance). Without this, a single near-full disk
// can be reported under several mount points all "above 90%", which prompts
// the agent to repeatedly re-investigate the same disk. When mounts collapse,
// the one matching preferredMounts is kept.
func DedupeDisks(disks []DiskStat) []DiskStat {
	out := make([]DiskStat, 0, len(disks))
	for _, d := range disks {
		if d.FSType == "devfs" {
			continue
		}
		dupIdx := -1
		for i, o := range out {
			if sameStorage(o, d) {
				dupIdx = i
				break
			}
		}
		if dupIdx == -1 {
			out = append(out, d)
			continue
		}
		if isPreferredMount(d.Mount) && !isPreferredMount(out[dupIdx].Mount) {
			out[dupIdx] = d
		}
	}
	return out
}

// sameStorage reports whether a and b appear to report usage for the same
// underlying storage: equal total capacity and free space within
// dedupeFreeTolerance of that total.
func sameStorage(a, b DiskStat) bool {
	if a.Total == 0 || a.Total != b.Total {
		return false
	}
	var diff uint64
	if a.Free > b.Free {
		diff = a.Free - b.Free
	} else {
		diff = b.Free - a.Free
	}
	return float64(diff)/float64(a.Total) < dedupeFreeTolerance
}

// preferredMounts ranks the mounts most relevant to a sysadmin checking disk
// space, used to pick one representative mount when several mounts collapse
// to the same underlying storage.
var preferredMounts = []string{"/", "/System/Volumes/Data"}

func isPreferredMount(mount string) bool {
	for _, p := range preferredMounts {
		if mount == p {
			return true
		}
	}
	return false
}

// topLargestCount is the number of largest directories and files reported
// per disk.
const topLargestCount = 3

// BuildReport composes a human-readable summary of disk usage.
func BuildReport(disks []DiskStat) string {
	return buildReport(disks, collectSmartReports(disks))
}

// collectSmartReports runs CheckSmart once per distinct device.
func collectSmartReports(disks []DiskStat) map[string][]SmartReport {
	cache := make(map[string][]SmartReport)
	for _, d := range disks {
		if d.Device == "" {
			continue
		}
		if _, ok := cache[d.Device]; !ok {
			slog.Debug("check_drives: checking SMART status", "device", d.Device)
			cache[d.Device] = CheckSmart(d.Device)
		}
	}
	return cache
}

// buildReport composes a human-readable summary of disk usage, using
// pre-fetched SMART reports keyed by device.
func buildReport(disks []DiskStat, smartCache map[string][]SmartReport) string {
	var sb strings.Builder
	for i, d := range disks {
		fmt.Fprintf(&sb, "Mount:      %s\n", d.Mount)
		if d.FSType != "" {
			fmt.Fprintf(&sb, "Filesystem: %s\n", d.FSType)
		}
		fmt.Fprintf(&sb, "Total:      %s\n", formatBytes(d.Total))
		fmt.Fprintf(&sb, "Used:       %s (%.1f%%)\n", formatBytes(d.Used()), d.UsedPct())
		fmt.Fprintf(&sb, "Free:       %s\n", formatBytes(d.Free))

		if d.Device != "" {
			reports := smartCache[d.Device]
			if len(reports) == 0 {
				slog.Debug("check_drives: SMART status unavailable", "device", d.Device)
			}
			for _, sr := range reports {
				if !sr.Available {
					slog.Debug("check_drives: SMART status unavailable", "device", sr.Device)
					continue
				}
				status := "PASSED"
				if !sr.Healthy {
					status = "FAILED"
				}
				fmt.Fprintf(&sb, "SMART:      %s (%s)\n", status, sr.Device)
				for _, finding := range sr.Findings {
					fmt.Fprintf(&sb, "  %s\n", finding)
				}
			}
		}

		slog.Debug("check_drives: scanning for largest entries", "mount", d.Mount)
		dirs, files, err := TopLargest(d.Mount, topLargestCount)
		if err != nil {
			slog.Debug("check_drives: scan failed", "mount", d.Mount, "error", err)
		} else {
			slog.Debug("check_drives: scan complete", "mount", d.Mount, "top_dirs", len(dirs), "top_files", len(files))
			if len(dirs) > 0 {
				fmt.Fprintf(&sb, "Top directories:\n")
				for _, dir := range dirs {
					fmt.Fprintf(&sb, "  %s (%s)\n", dir.Path, formatBytes(dir.Size))
				}
			}
			if len(files) > 0 {
				fmt.Fprintf(&sb, "Top files:\n")
				for _, f := range files {
					fmt.Fprintf(&sb, "  %s (%s)\n", f.Path, formatBytes(f.Size))
				}
			}
		}

		if i < len(disks)-1 {
			fmt.Fprintf(&sb, "\n")
		}
	}
	return sb.String()
}

func formatBytes(b uint64) string {
	switch {
	case b >= 1<<40:
		return fmt.Sprintf("%.2f TB", float64(b)/(1<<40))
	case b >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.2f MB", float64(b)/(1<<20))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
