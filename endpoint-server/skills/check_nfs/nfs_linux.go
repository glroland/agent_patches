//go:build linux

package check_nfs

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// nfsSupported reports whether full NFS monitoring is available on this platform.
func nfsSupported() bool { return true }

// NFSMount is a parsed entry from /proc/mounts for an NFS filesystem.
type NFSMount struct {
	Mount  string // local mount point
	Remote string // server:export
}

// listNFSMounts returns all NFS (nfs, nfs4) mounts from /proc/mounts.
func listNFSMounts() ([]NFSMount, error) {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return nil, fmt.Errorf("check_nfs: open /proc/mounts: %w", err)
	}
	defer f.Close()

	var mounts []NFSMount
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		// device mountpoint fstype options dump pass
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		fstype := fields[2]
		if fstype != "nfs" && fstype != "nfs4" {
			continue
		}
		mounts = append(mounts, NFSMount{Mount: fields[1], Remote: fields[0]})
	}
	return mounts, sc.Err()
}

// gatherStats reads /proc/self/mountstats and assembles NFSMountStats for each
// known NFS mount. Mounts not found in mountstats get zero metrics.
func gatherStats(_ context.Context, mounts []NFSMount) ([]NFSMountStats, error) {
	data, err := os.ReadFile("/proc/self/mountstats")
	if err != nil {
		// Not fatal; return empty stats with just the mount paths.
		result := make([]NFSMountStats, len(mounts))
		for i, m := range mounts {
			result[i] = NFSMountStats{Mount: m.Mount, Remote: m.Remote}
		}
		return result, fmt.Errorf("check_nfs: read /proc/self/mountstats: %w", err)
	}

	parsed := ParseMountstats(string(data))

	result := make([]NFSMountStats, len(mounts))
	for i, m := range mounts {
		s := NFSMountStats{Mount: m.Mount, Remote: m.Remote}
		if raw, ok := parsed[m.Mount]; ok {
			s.PendingOps = raw.PendingOps
			s.GETATTRMs = raw.GETATTRMs
		}
		result[i] = s
	}
	return result, nil
}

// countDStateProcs counts processes in uninterruptible sleep (D state) that
// are blocked in an NFS or RPC kernel wait channel.
func countDStateProcs() int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}

	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid := e.Name()
		// Only numeric directories are processes.
		if len(pid) == 0 || pid[0] < '0' || pid[0] > '9' {
			continue
		}

		status, err := os.ReadFile(filepath.Join("/proc", pid, "status"))
		if err != nil {
			continue
		}
		if !bytes.Contains(status, []byte("State:\tD")) {
			continue
		}

		// Check kernel wait channel for NFS/RPC relevance.
		wchan, err := os.ReadFile(filepath.Join("/proc", pid, "wchan"))
		if err != nil {
			continue
		}
		wc := strings.TrimSpace(string(wchan))
		if strings.Contains(wc, "nfs") || strings.Contains(wc, "rpc") ||
			strings.Contains(wc, "sunrpc") {
			count++
		}
	}
	return count
}

// lazyUnmount performs a lazy unmount of the given mount point using
// "umount -l", which detaches it immediately and cleans up references
// as they are released. This is the safe way to handle a hung NFS mount.
func lazyUnmount(ctx context.Context, mount string) error {
	// umount requires root; use sudo -n when running as a non-root user so
	// the /etc/sudoers.d/agent_patches allowlist applies.
	var cmd *exec.Cmd
	if os.Getuid() != 0 {
		cmd = exec.CommandContext(ctx, "sudo", "-n", "umount", "-l", mount) //nolint:gosec
	} else {
		cmd = exec.CommandContext(ctx, "umount", "-l", mount) //nolint:gosec
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("umount -l %s: %w: %s", mount, err, strings.TrimSpace(string(out)))
	}
	return nil
}
