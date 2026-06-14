//go:build !linux && !darwin

package check_drives

// deviceID is not implemented on this platform; mount-boundary detection is
// skipped (ok is always false), so all directories are scanned regardless of
// mount point.
func deviceID(_ string) (id uint64, ok bool) {
	return 0, false
}
