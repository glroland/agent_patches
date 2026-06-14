//go:build darwin

package check_drives

import "regexp"

// partitionRe matches a macOS partition device path and captures the parent
// physical disk: /dev/disk0s1 -> /dev/disk0.
var partitionRe = regexp.MustCompile(`^(/dev/disk\d+)s\d+$`)

// parentDevices resolves a partition device path to its parent physical disk.
func parentDevices(device string) []string {
	if device == "" {
		return nil
	}
	if m := partitionRe.FindStringSubmatch(device); m != nil {
		return []string{m[1]}
	}
	return []string{device}
}
