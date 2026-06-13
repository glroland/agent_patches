//go:build darwin

package check_drives

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func localDisks() ([]DiskStat, error) {
	// First call with nil buffer to obtain the mount count.
	n, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil {
		return nil, fmt.Errorf("disk_monitor: getfsstat count: %w", err)
	}
	if n == 0 {
		return nil, nil
	}

	buf := make([]unix.Statfs_t, n)
	n, err = unix.Getfsstat(buf, unix.MNT_NOWAIT)
	if err != nil {
		return nil, fmt.Errorf("disk_monitor: getfsstat: %w", err)
	}

	var disks []DiskStat
	for i := range buf[:n] {
		m := &buf[i]
		// MNT_LOCAL indicates a locally-attached filesystem; skips NFS, SMB, etc.
		if m.Flags&unix.MNT_LOCAL == 0 {
			continue
		}
		disks = append(disks, DiskStat{
			Mount:  unix.ByteSliceToString(m.Mntonname[:]),
			Total:  uint64(m.Bsize) * m.Blocks,
			Free:   uint64(m.Bsize) * m.Bfree,
			FSType: unix.ByteSliceToString(m.Fstypename[:]),
		})
	}
	return disks, nil
}
