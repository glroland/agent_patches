// Package sysinfo gathers static metadata about the host: operating system,
// distribution, version, kernel, CPU, memory, physical disks, and network
// interfaces. The data is static for the lifetime of the process, so it is
// gathered once and reused.
package capture_system_info

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"agent_patches/endpoint-server/a2a/tool"
)

// Disk describes one physical block device.
type Disk struct {
	Device    string // e.g. "sda", "nvme0n1", "disk0"
	SizeBytes uint64
	Model     string
}

// NetInterface describes one network interface.
type NetInterface struct {
	Name      string
	MAC       string
	SpeedMbps int // 0 if unknown
}

// Info is a snapshot of static host metadata.
type Info struct {
	OS            string // runtime.GOOS: linux, darwin, windows
	Distribution  string // e.g. "Ubuntu 22.04.3 LTS", "macOS", "Windows 11 Pro"
	Version       string // OS/distribution version
	Kernel        string // kernel release string
	Hostname      string
	CPUModel      string
	CPUCores      int
	MemoryBytes   uint64
	Disks         []Disk
	NetInterfaces []NetInterface
}

// Gather collects static host metadata, exported so callers (main, the
// status handler) can obtain the Info struct directly without going through
// the tool interface.
func Gather() (Info, error) {
	return gather()
}

type systemInfoInput struct{}

// NewSystemInfoTool returns a task tool that reports static metadata about
// the host: OS, distribution, version, kernel, CPU, memory, physical disks,
// and network interfaces.
func NewSystemInfoTool() (tool.Tool, error) {
	return tool.New(
		"capture_system_info",
		"Reports static metadata about the host, including operating system, "+
			"distribution, version, kernel, CPU model and core count, total memory, "+
			"physical disks (device, size, model), and network interfaces "+
			"(name, MAC address, link speed).",
		func(_ context.Context, _ systemInfoInput) (string, error) {
			slog.Info("capture_system_info: starting")
			info, err := gather()
			if err != nil {
				slog.Info("capture_system_info: failed", "error", err)
				return "", fmt.Errorf("system_info: %w", err)
			}
			slog.Debug("capture_system_info: gathered host metadata",
				"os", info.OS, "distribution", info.Distribution, "version", info.Version,
				"cpu_cores", info.CPUCores, "disks", len(info.Disks), "net_interfaces", len(info.NetInterfaces))
			report := BuildReport(info)
			slog.Info("capture_system_info: completed", "output_len", len(report))
			return report, nil
		},
	)
}

// BuildReport composes a human-readable summary of host metadata.
func BuildReport(info Info) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "Hostname:     %s\n", info.Hostname)
	fmt.Fprintf(&sb, "OS:           %s\n", info.OS)
	if info.Distribution != "" {
		fmt.Fprintf(&sb, "Distribution: %s\n", info.Distribution)
	}
	if info.Version != "" {
		fmt.Fprintf(&sb, "Version:      %s\n", info.Version)
	}
	if info.Kernel != "" {
		fmt.Fprintf(&sb, "Kernel:       %s\n", info.Kernel)
	}
	if info.CPUModel != "" {
		fmt.Fprintf(&sb, "CPU:          %s (%d cores)\n", info.CPUModel, info.CPUCores)
	}
	if info.MemoryBytes > 0 {
		fmt.Fprintf(&sb, "Memory:       %s\n", formatBytes(info.MemoryBytes))
	}

	if len(info.Disks) > 0 {
		fmt.Fprintf(&sb, "\nDisks:\n")
		for _, d := range info.Disks {
			if d.Model != "" {
				fmt.Fprintf(&sb, "  %-12s %10s  (%s)\n", d.Device, formatBytes(d.SizeBytes), d.Model)
			} else {
				fmt.Fprintf(&sb, "  %-12s %10s\n", d.Device, formatBytes(d.SizeBytes))
			}
		}
	}

	if len(info.NetInterfaces) > 0 {
		fmt.Fprintf(&sb, "\nNetwork Interfaces:\n")
		for _, n := range info.NetInterfaces {
			speed := "unknown"
			if n.SpeedMbps > 0 {
				speed = fmt.Sprintf("%d Mbps", n.SpeedMbps)
			}
			fmt.Fprintf(&sb, "  %-12s MAC=%-17s Speed=%s\n", n.Name, n.MAC, speed)
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
