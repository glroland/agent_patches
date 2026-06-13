//go:build windows

package memoryusage

import (
	"fmt"
	"syscall"
	"unsafe"
)

// memoryStatusEx mirrors the Windows MEMORYSTATUSEX structure.
type memoryStatusEx struct {
	dwLength                uint32
	dwMemoryLoad            uint32
	ullTotalPhys            uint64
	ullAvailPhys            uint64
	ullTotalPageFile        uint64
	ullAvailPageFile        uint64
	ullTotalVirtual         uint64
	ullAvailVirtual         uint64
	ullAvailExtendedVirtual uint64
}

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	globalMemoryStatusExProc = kernel32.NewProc("GlobalMemoryStatusEx")
)

func localMemory() (MemStat, error) {
	var ms memoryStatusEx
	ms.dwLength = uint32(unsafe.Sizeof(ms))
	ret, _, err := globalMemoryStatusExProc.Call(uintptr(unsafe.Pointer(&ms)))
	if ret == 0 {
		return MemStat{}, fmt.Errorf("memory_monitor: GlobalMemoryStatusEx: %w", err)
	}

	// The page file total includes physical RAM; subtract to isolate swap space.
	var swapTotal, swapFree uint64
	if ms.ullTotalPageFile > ms.ullTotalPhys {
		swapTotal = ms.ullTotalPageFile - ms.ullTotalPhys
		if ms.ullAvailPageFile > ms.ullAvailPhys {
			swapFree = ms.ullAvailPageFile - ms.ullAvailPhys
		}
	}

	return MemStat{
		Total:     ms.ullTotalPhys,
		Available: ms.ullAvailPhys,
		SwapTotal: swapTotal,
		SwapFree:  swapFree,
	}, nil
}
