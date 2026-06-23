//go:build !linux

package check_nfs

import (
	"context"
	"fmt"
)

// nfsSupported reports whether full NFS monitoring is available on this platform.
func nfsSupported() bool { return false }

// NFSMount is a parsed NFS mount entry. On non-Linux platforms the list is
// always empty; the type is defined here for cross-platform compilation.
type NFSMount struct {
	Mount  string
	Remote string
}

func listNFSMounts() ([]NFSMount, error) { return nil, nil }

func gatherStats(_ context.Context, _ []NFSMount) ([]NFSMountStats, error) { return nil, nil }

func countDStateProcs() int { return 0 }

func lazyUnmount(_ context.Context, mount string) error {
	return fmt.Errorf("lazy unmount not supported on this platform (mount: %s)", mount)
}
