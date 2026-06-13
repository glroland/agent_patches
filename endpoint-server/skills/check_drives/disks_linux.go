//go:build linux

package check_drives

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"
)

// localFSTypes enumerates filesystem types that represent physical local storage.
// Virtual/pseudo filesystems (tmpfs, proc, sysfs, cgroup, devtmpfs, etc.) are excluded.
var localFSTypes = map[string]bool{
	"ext2": true, "ext3": true, "ext4": true,
	"xfs": true, "btrfs": true, "f2fs": true,
	"vfat": true, "exfat": true,
	"zfs": true, "reiserfs": true, "jfs": true,
	"fuseblk": true, // NTFS via ntfs-3g on Linux
}

func localDisks() ([]DiskStat, error) {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return nil, fmt.Errorf("disk_monitor: open /proc/mounts: %w", err)
	}
	defer f.Close()

	seen := make(map[string]bool)
	var disks []DiskStat

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		// Format: device mountpoint fstype options dump pass
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		fsType := fields[2]
		if !localFSTypes[fsType] {
			continue
		}
		mount := fields[1]
		if seen[mount] {
			continue
		}
		seen[mount] = true

		var st syscall.Statfs_t
		if err := syscall.Statfs(mount, &st); err != nil {
			continue // skip inaccessible paths
		}
		bsize := uint64(st.Bsize) //nolint:gosec // block size is always positive
		disks = append(disks, DiskStat{
			Mount:  mount,
			Total:  bsize * st.Blocks,
			Free:   bsize * st.Bfree,
			FSType: fsType,
		})
	}
	if err := scanner.Err(); err != nil {
		return disks, fmt.Errorf("disk_monitor: reading /proc/mounts: %w", err)
	}
	return disks, nil
}
