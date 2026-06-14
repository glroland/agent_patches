//go:build linux || darwin

package check_drives

import (
	"os"
	"syscall"
)

// deviceID returns the filesystem device ID for path, used to detect mount
// point boundaries (e.g. an NFS share mounted inside a local directory tree).
// ok is false if the device ID could not be determined.
func deviceID(path string) (id uint64, ok bool) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true //nolint:unconvert,gosec
}
