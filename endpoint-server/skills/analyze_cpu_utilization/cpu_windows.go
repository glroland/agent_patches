//go:build windows

package analyze_cpu_utilization

import (
	"context"
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	getSystemTimesProc = kernel32.NewProc("GetSystemTimes")
)

// filetime mirrors the Windows FILETIME structure (100-nanosecond intervals
// since January 1, 1601).
type filetime struct {
	LowDateTime  uint32
	HighDateTime uint32
}

func (f filetime) uint64() uint64 {
	return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime)
}

func localCPU(ctx context.Context) (CPUStat, error) {
	idle1, kernel1, user1, err := getSystemTimes()
	if err != nil {
		return CPUStat{}, err
	}

	select {
	case <-time.After(time.Second):
	case <-ctx.Done():
		return CPUStat{}, ctx.Err()
	}

	idle2, kernel2, user2, err := getSystemTimes()
	if err != nil {
		return CPUStat{}, err
	}

	// KernelTime includes IdleTime, so subtract to get pure kernel time.
	idleDelta := float64(idle2.uint64() - idle1.uint64())
	// Total = KernelTime + UserTime (KernelTime already includes IdleTime).
	totalDelta := float64((kernel2.uint64() + user2.uint64()) - (kernel1.uint64() + user1.uint64()))

	var usedPct float64
	if totalDelta > 0 {
		usedPct = (1 - idleDelta/totalDelta) * 100
	}

	return CPUStat{
		UsedPct: usedPct,
		NumCPU:  runtime.NumCPU(),
		// Load averages are not a Windows concept; leave at zero.
	}, nil
}

func getSystemTimes() (idle, kernel, user filetime, err error) {
	ret, _, e := getSystemTimesProc.Call(
		uintptr(unsafe.Pointer(&idle)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if ret == 0 {
		err = fmt.Errorf("cpu_usage: GetSystemTimes: %w", e)
	}
	return
}
