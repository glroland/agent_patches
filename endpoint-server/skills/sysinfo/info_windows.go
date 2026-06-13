//go:build windows

package sysinfo

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func gather() (Info, error) {
	info := Info{OS: "windows"}

	if h, err := os.Hostname(); err == nil {
		info.Hostname = h
	}

	if caption := psValue("(Get-CimInstance Win32_OperatingSystem).Caption"); caption != "" {
		info.Distribution = caption
	}
	if version := psValue("(Get-CimInstance Win32_OperatingSystem).Version"); version != "" {
		info.Version = version
		info.Kernel = version
	}
	if cpu := psValue("(Get-CimInstance Win32_Processor).Name"); cpu != "" {
		info.CPUModel = cpu
	}
	if cores := psValue("(Get-CimInstance Win32_Processor).NumberOfLogicalProcessors"); cores != "" {
		info.CPUCores, _ = strconv.Atoi(cores)
	}
	if mem := psValue("(Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory"); mem != "" {
		info.MemoryBytes, _ = strconv.ParseUint(mem, 10, 64)
	}

	info.Disks = windowsDisks()
	info.NetInterfaces = windowsNetInterfaces()

	return info, nil
}

// psValue runs a PowerShell expression and returns its trimmed output.
func psValue(expr string) string {
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", expr).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// psLines runs a PowerShell expression and returns its output split into
// non-empty, trimmed lines.
func psLines(expr string) []string {
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", expr).Output()
	if err != nil {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// windowsDisks lists physical disks via Win32_DiskDrive, one
// "DeviceID|SizeBytes|Model" line per disk.
func windowsDisks() []Disk {
	const script = `Get-CimInstance Win32_DiskDrive | ForEach-Object { "$($_.DeviceID)|$($_.Size)|$($_.Model)" }`

	var disks []Disk
	for _, line := range psLines(script) {
		fields := strings.SplitN(line, "|", 3)
		if len(fields) != 3 {
			continue
		}
		size, _ := strconv.ParseUint(fields[1], 10, 64)
		disks = append(disks, Disk{Device: fields[0], SizeBytes: size, Model: fields[2]})
	}
	return disks
}

// windowsNetInterfaces lists physical network adapters via Win32_NetworkAdapter,
// one "Name|MACAddress|SpeedBps" line per adapter.
func windowsNetInterfaces() []NetInterface {
	const script = `Get-CimInstance Win32_NetworkAdapter -Filter "PhysicalAdapter=true" | ForEach-Object { "$($_.Name)|$($_.MACAddress)|$($_.Speed)" }`

	var ifaces []NetInterface
	for _, line := range psLines(script) {
		fields := strings.SplitN(line, "|", 3)
		if len(fields) != 3 {
			continue
		}
		speedMbps := 0
		if bps, err := strconv.ParseUint(fields[2], 10, 64); err == nil {
			speedMbps = int(bps / 1_000_000)
		}
		ifaces = append(ifaces, NetInterface{Name: fields[0], MAC: fields[1], SpeedMbps: speedMbps})
	}
	return ifaces
}
