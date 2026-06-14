//go:build linux

package check_drives

import (
	"os"
	"path/filepath"
	"regexp"
)

// partitionRe matches a Linux partition device path and captures the parent
// physical disk: /dev/sda1 -> /dev/sda, /dev/nvme0n1p1 -> /dev/nvme0n1,
// /dev/mmcblk0p1 -> /dev/mmcblk0.
var partitionRe = regexp.MustCompile(`^(/dev/(?:[shv]d[a-z]+|nvme\d+n\d+|mmcblk\d+))(?:p?\d+)?$`)

// parentDevices resolves device to the underlying physical disk(s).
// Plain disks and partitions resolve directly via partitionRe. Device-mapper
// devices (LVM, dm-crypt, software RAID) are resolved by walking
// /sys/class/block/<dm>/slaves, recursing through any layered mappings until
// physical disks are reached. Devices that cannot be resolved to a physical
// disk are omitted.
func parentDevices(device string) []string {
	var results []string
	seen := make(map[string]bool)

	var visit func(string)
	visit = func(dev string) {
		if seen[dev] {
			return
		}
		seen[dev] = true

		if m := partitionRe.FindStringSubmatch(dev); m != nil {
			for _, r := range results {
				if r == m[1] {
					return
				}
			}
			results = append(results, m[1])
			return
		}

		for _, slave := range dmSlaves(dev) {
			visit(slave)
		}
	}
	visit(device)
	return results
}

// dmSlaves returns the underlying devices backing a device-mapper device
// (e.g. /dev/mapper/vg-root -> /dev/dm-0 -> sda3), or nil if dev is not a
// device-mapper device or has no discoverable slaves.
func dmSlaves(dev string) []string {
	real, err := filepath.EvalSymlinks(dev)
	if err != nil {
		real = dev
	}
	entries, err := os.ReadDir(filepath.Join("/sys/class/block", filepath.Base(real), "slaves"))
	if err != nil {
		return nil
	}
	slaves := make([]string, 0, len(entries))
	for _, e := range entries {
		slaves = append(slaves, "/dev/"+e.Name())
	}
	return slaves
}
