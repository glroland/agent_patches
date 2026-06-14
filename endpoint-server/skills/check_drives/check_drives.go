package check_drives

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"agent_patches/endpoint-server/a2a/tool"
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
// for all local disks on the host.
func NewDiskUsageTool() (tool.Tool, error) {
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
				return "", fmt.Errorf("disk_usage: %w", err)
			}
			slog.Debug("check_drives: found local disks", "count", len(disks))
			if len(disks) == 0 {
				slog.Info("check_drives: completed", "disks", 0)
				return "No local disks found.", nil
			}
			report := BuildReport(disks)
			slog.Info("check_drives: completed", "disks", len(disks), "output_len", len(report))
			return report, nil
		},
	)
}

// topLargestCount is the number of largest directories and files reported
// per disk.
const topLargestCount = 3

// BuildReport composes a human-readable summary of disk usage.
func BuildReport(disks []DiskStat) string {
	var sb strings.Builder
	smartCache := make(map[string][]SmartReport)
	for i, d := range disks {
		fmt.Fprintf(&sb, "Mount:      %s\n", d.Mount)
		if d.FSType != "" {
			fmt.Fprintf(&sb, "Filesystem: %s\n", d.FSType)
		}
		fmt.Fprintf(&sb, "Total:      %s\n", formatBytes(d.Total))
		fmt.Fprintf(&sb, "Used:       %s (%.1f%%)\n", formatBytes(d.Used()), d.UsedPct())
		fmt.Fprintf(&sb, "Free:       %s\n", formatBytes(d.Free))

		if d.Device != "" {
			reports, ok := smartCache[d.Device]
			if !ok {
				slog.Debug("check_drives: checking SMART status", "device", d.Device)
				reports = CheckSmart(d.Device)
				smartCache[d.Device] = reports
			}
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
