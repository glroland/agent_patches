//go:build darwin

package capture_system_info

import (
	"log/slog"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func gather() (Info, error) {
	info := Info{OS: "darwin", Distribution: "macOS"}

	if h, err := os.Hostname(); err == nil {
		info.Hostname = h
	}
	if v, err := unix.Sysctl("kern.osproductversion"); err == nil {
		info.Version = v
	}
	if k, err := unix.Sysctl("kern.osrelease"); err == nil {
		info.Kernel = k
	}
	if cpu, err := unix.Sysctl("machdep.cpu.brand_string"); err == nil {
		info.CPUModel = cpu
	}
	if n, err := unix.SysctlUint32("hw.physicalcpu"); err == nil {
		info.CPUCores = int(n)
	}
	if mem, err := unix.SysctlUint64("hw.memsize"); err == nil {
		info.MemoryBytes = mem
	}

	info.Disks = diskutilDisks()
	info.NetInterfaces = darwinNetInterfaces()

	return info, nil
}

var (
	diskutilIdentifierRe = regexp.MustCompile(`(?m)^\s*Device Identifier:\s*(\S+)`)
	diskutilNameRe       = regexp.MustCompile(`(?m)^\s*Device / Media Name:\s*(.+)`)
	diskutilSizeRe       = regexp.MustCompile(`(?m)^\s*Disk Size:.*\((\d+) Bytes\)`)
	diskutilWholeRe      = regexp.MustCompile(`(?m)^\s*Whole:\s*(Yes|No)`)
	diskutilVirtualRe    = regexp.MustCompile(`(?m)^\s*Virtual:\s*(Yes|No)`)
)

// diskutilDisks shells out to `diskutil info -all` and extracts whole,
// non-virtual physical disks.
func diskutilDisks() []Disk {
	slog.Info("capture_system_info: running command", "command", "diskutil info -all")
	out, err := exec.Command("diskutil", "info", "-all").Output()
	if err != nil {
		slog.Info("capture_system_info: command failed", "command", "diskutil info -all", "error", err)
		return nil
	}
	slog.Info("capture_system_info: command finished", "command", "diskutil info -all", "output_len", len(out))

	var disks []Disk
	for _, block := range strings.Split(string(out), "**********") {
		whole := diskutilWholeRe.FindStringSubmatch(block)
		if len(whole) < 2 || whole[1] != "Yes" {
			continue
		}
		// Skip virtual whole disks (e.g. APFS snapshot/recovery volumes);
		// disks with no "Virtual:" field at all are physical.
		if virtual := diskutilVirtualRe.FindStringSubmatch(block); len(virtual) == 2 && virtual[1] == "Yes" {
			continue
		}

		id := diskutilIdentifierRe.FindStringSubmatch(block)
		if len(id) < 2 {
			continue
		}

		var size uint64
		if m := diskutilSizeRe.FindStringSubmatch(block); len(m) == 2 {
			size, _ = strconv.ParseUint(m[1], 10, 64)
		}

		model := ""
		if m := diskutilNameRe.FindStringSubmatch(block); len(m) == 2 {
			model = strings.TrimSpace(m[1])
		}

		disks = append(disks, Disk{Device: id[1], SizeBytes: size, Model: model})
	}
	return disks
}

var ifconfigSpeedRe = regexp.MustCompile(`media:\s+\S+\s+\((\d+)base`)

// darwinNetInterfaces enumerates non-loopback network interfaces and best-effort
// link speed via `ifconfig <name>`.
func darwinNetInterfaces() []NetInterface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var out []NetInterface
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagLoopback != 0 {
			continue
		}

		speed := 0
		slog.Info("capture_system_info: running command", "command", "ifconfig "+ifi.Name)
		if data, err := exec.Command("ifconfig", ifi.Name).Output(); err == nil {
			slog.Info("capture_system_info: command finished", "command", "ifconfig "+ifi.Name, "output_len", len(data))
			if m := ifconfigSpeedRe.FindStringSubmatch(string(data)); len(m) == 2 {
				speed, _ = strconv.Atoi(m[1])
			}
		} else {
			slog.Info("capture_system_info: command failed", "command", "ifconfig "+ifi.Name, "error", err)
		}

		out = append(out, NetInterface{
			Name:      ifi.Name,
			MAC:       ifi.HardwareAddr.String(),
			SpeedMbps: speed,
		})
	}
	return out
}
