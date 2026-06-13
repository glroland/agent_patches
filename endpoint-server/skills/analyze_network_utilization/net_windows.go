//go:build windows

package analyze_network_utilization

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// snapshot returns cumulative (bytesIn, bytesOut) summed across all
// non-loopback, up adapters using GetAdaptersInfo and GetIfEntry2Ex.
func snapshot() (uint64, uint64, error) {
	// First call determines required buffer size.
	var size uint32
	err := windows.GetAdaptersInfo(nil, &size)
	if err != nil && err != windows.ERROR_BUFFER_OVERFLOW {
		return 0, 0, fmt.Errorf("netmon: GetAdaptersInfo size: %w", err)
	}
	buf := make([]byte, size)
	ai := (*windows.IpAdapterInfo)(unsafe.Pointer(&buf[0]))
	if err := windows.GetAdaptersInfo(ai, &size); err != nil {
		return 0, 0, fmt.Errorf("netmon: GetAdaptersInfo: %w", err)
	}

	var bytesIn, bytesOut uint64
	for ai != nil {
		if ai.Type != windows.IF_TYPE_SOFTWARE_LOOPBACK {
			var row windows.MibIfRow2
			row.InterfaceIndex = ai.Index
			if err := windows.GetIfEntry2Ex(windows.MibIfEntryNormal, &row); err == nil &&
				row.OperStatus == windows.IfOperStatusUp {
				bytesIn += row.InOctets
				bytesOut += row.OutOctets
			}
		}
		ai = ai.Next
	}
	return bytesIn, bytesOut, nil
}
