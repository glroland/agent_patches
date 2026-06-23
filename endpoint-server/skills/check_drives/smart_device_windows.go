//go:build windows

package check_drives

// parentDevices on Windows receives a "PhysicalDriveN" path set by localDisks()
// and returns it as-is; no device-mapper resolution is needed.
func parentDevices(device string) []string {
	if device == "" {
		return nil
	}
	return []string{device}
}
