//go:build darwin

package memmon

import (
	"encoding/binary"
	"fmt"

	"golang.org/x/sys/unix"
)

func localMemory() (MemStat, error) {
	totalMem, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return MemStat{}, fmt.Errorf("memory_monitor: hw.memsize: %w", err)
	}

	pageSize, err := unix.SysctlUint32("hw.pagesize")
	if err != nil {
		return MemStat{}, fmt.Errorf("memory_monitor: hw.pagesize: %w", err)
	}

	freePages, err := unix.SysctlUint32("vm.page_free_count")
	if err != nil {
		return MemStat{}, fmt.Errorf("memory_monitor: vm.page_free_count: %w", err)
	}

	// Speculative pages are prefetch cache that the kernel reclaims on demand.
	specPages, _ := unix.SysctlUint32("vm.page_speculative_count")

	available := (uint64(freePages) + uint64(specPages)) * uint64(pageSize)

	// vm.swapusage returns an xsw_usage struct: total(8), avail(8), used(8), ...
	var swapTotal, swapFree uint64
	if raw, err := unix.SysctlRaw("vm.swapusage"); err == nil && len(raw) >= 16 {
		swapTotal = binary.LittleEndian.Uint64(raw[0:8])
		swapFree = binary.LittleEndian.Uint64(raw[8:16])
	}

	return MemStat{
		Total:     totalMem,
		Available: available,
		SwapTotal: swapTotal,
		SwapFree:  swapFree,
	}, nil
}
