package diskusage

import (
	"context"
	"fmt"
	"strings"

	"agent_patches/endpoint-server/a2a/tool"
)

// DiskStat holds usage statistics for one local disk.
type DiskStat struct {
	Mount  string
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
		"disk_usage",
		"Reports current disk space usage for all local disks on the host, "+
			"including total, used, and free space per mount point, along with "+
			"the top largest directories and files on each disk.",
		func(_ context.Context, _ diskUsageInput) (string, error) {
			disks, err := localDisks()
			if err != nil {
				return "", fmt.Errorf("disk_usage: %w", err)
			}
			if len(disks) == 0 {
				return "No local disks found.", nil
			}
			return BuildReport(disks), nil
		},
	)
}

// topLargestCount is the number of largest directories and files reported
// per disk.
const topLargestCount = 3

// BuildReport composes a human-readable summary of disk usage.
func BuildReport(disks []DiskStat) string {
	var sb strings.Builder
	for i, d := range disks {
		fmt.Fprintf(&sb, "Mount:      %s\n", d.Mount)
		if d.FSType != "" {
			fmt.Fprintf(&sb, "Filesystem: %s\n", d.FSType)
		}
		fmt.Fprintf(&sb, "Total:      %s\n", formatBytes(d.Total))
		fmt.Fprintf(&sb, "Used:       %s (%.1f%%)\n", formatBytes(d.Used()), d.UsedPct())
		fmt.Fprintf(&sb, "Free:       %s\n", formatBytes(d.Free))

		dirs, files, err := TopLargest(d.Mount, topLargestCount)
		if err == nil {
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
