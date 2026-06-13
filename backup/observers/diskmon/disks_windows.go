//go:build windows

package diskmon

import (
	"golang.org/x/sys/windows"
)

func localDisks() ([]DiskStat, error) {
	var disks []DiskStat

	for _, letter := range "ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
		root := string(letter) + ":\\"
		rootPtr, err := windows.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		// Only report fixed local drives; skip removable, network, CD-ROM, etc.
		if windows.GetDriveType(rootPtr) != windows.DRIVE_FIXED {
			continue
		}

		var freeBytesAvailable, totalBytes, totalFreeBytes uint64
		if err := windows.GetDiskFreeSpaceEx(rootPtr, &freeBytesAvailable, &totalBytes, &totalFreeBytes); err != nil {
			continue
		}
		disks = append(disks, DiskStat{
			Mount: root,
			Total: totalBytes,
			Free:  totalFreeBytes,
		})
	}
	return disks, nil
}
