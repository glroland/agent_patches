//go:build windows

package check_drives

// parentDevices is not implemented on Windows: resolving a drive letter to a
// \\.\PhysicalDriveN path requires WMI volume/disk associations. SMART
// checks are skipped on this platform.
func parentDevices(_ string) []string {
	return nil
}
