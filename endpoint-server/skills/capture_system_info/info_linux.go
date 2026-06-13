//go:build linux

package capture_system_info

import (
	"bufio"
	"net"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func gather() (Info, error) {
	info := Info{OS: "linux"}

	if h, err := os.Hostname(); err == nil {
		info.Hostname = h
	}

	info.Distribution, info.Version = osRelease()

	var uts unix.Utsname
	if err := unix.Uname(&uts); err == nil {
		info.Kernel = unix.ByteSliceToString(uts.Release[:])
	}

	info.CPUModel, info.CPUCores = cpuInfo()
	info.MemoryBytes = memTotal()
	info.Disks = blockDevices()
	info.NetInterfaces = linuxNetInterfaces()

	return info, nil
}

// osRelease parses /etc/os-release and returns (PRETTY_NAME, VERSION_ID).
// Falls back to (NAME, VERSION_ID) if PRETTY_NAME is absent.
func osRelease() (distribution, version string) {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return "", ""
	}
	defer f.Close()

	var name string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		key, val, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			continue
		}
		val = strings.Trim(val, `"`)
		switch key {
		case "PRETTY_NAME":
			distribution = val
		case "NAME":
			name = val
		case "VERSION_ID":
			version = val
		}
	}
	if distribution == "" {
		distribution = name
	}
	return distribution, version
}

// cpuInfo parses /proc/cpuinfo for the CPU model name and logical core count.
func cpuInfo() (model string, cores int) {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return "", 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		key, val, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "model name":
			if model == "" {
				model = val
			}
		case "processor":
			cores++
		}
	}
	return model, cores
}

// memTotal returns total physical RAM in bytes, parsed from /proc/meminfo.
func memTotal() uint64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		key, val, ok := strings.Cut(scanner.Text(), ":")
		if !ok || key != "MemTotal" {
			continue
		}
		fields := strings.Fields(val)
		if len(fields) == 0 {
			return 0
		}
		kb, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
}

// blockDevices enumerates physical block devices via /sys/block, skipping
// virtual devices (loop, ram, device-mapper, etc.).
func blockDevices() []Disk {
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return nil
	}

	var disks []Disk
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") ||
			strings.HasPrefix(name, "dm-") || strings.HasPrefix(name, "sr") {
			continue
		}

		sizeData, err := os.ReadFile("/sys/block/" + name + "/size")
		if err != nil {
			continue
		}
		sectors, err := strconv.ParseUint(strings.TrimSpace(string(sizeData)), 10, 64)
		if err != nil {
			continue
		}

		model := ""
		if data, err := os.ReadFile("/sys/block/" + name + "/device/model"); err == nil {
			model = strings.TrimSpace(string(data))
		}

		disks = append(disks, Disk{
			Device:    name,
			SizeBytes: sectors * 512,
			Model:     model,
		})
	}
	return disks
}

// linuxNetInterfaces enumerates non-loopback network interfaces and their
// link speed, read from /sys/class/net/<name>/speed.
func linuxNetInterfaces() []NetInterface {
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
		if data, err := os.ReadFile("/sys/class/net/" + ifi.Name + "/speed"); err == nil {
			if mbps, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && mbps > 0 {
				speed = mbps
			}
		}

		out = append(out, NetInterface{
			Name:      ifi.Name,
			MAC:       ifi.HardwareAddr.String(),
			SpeedMbps: speed,
		})
	}
	return out
}
