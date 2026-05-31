//go:build darwin

package netmon

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// snapshot returns cumulative (bytesIn, bytesOut) summed across all
// non-loopback interfaces using the NET_RT_IFLIST2 routing socket sysctl,
// which returns RTM_IFINFO2 messages containing 64-bit IfData64 counters.
func snapshot() (uint64, uint64, error) {
	// MIB: {CTL_NET=4, AF_ROUTE=17, 0, AF_UNSPEC=0, NET_RT_IFLIST2=6, 0}
	data, err := unix.SysctlRaw("net",
		unix.AF_ROUTE, 0, unix.AF_UNSPEC, unix.NET_RT_IFLIST2, 0)
	if err != nil {
		return 0, 0, fmt.Errorf("netmon: sysctl NET_RT_IFLIST2: %w", err)
	}

	var bytesIn, bytesOut uint64
	for len(data) >= 4 {
		msgLen := int(binary.LittleEndian.Uint16(data[0:2]))
		if msgLen < 4 || msgLen > len(data) {
			break
		}
		if data[3] == unix.RTM_IFINFO2 && msgLen >= unix.SizeofIfMsghdr2 {
			// Copy into a properly aligned struct before field access.
			var hdr unix.IfMsghdr2
			copy((*[unix.SizeofIfMsghdr2]byte)(unsafe.Pointer(&hdr))[:],
				data[:unix.SizeofIfMsghdr2])
			if hdr.Flags&unix.IFF_LOOPBACK == 0 && hdr.Flags&unix.IFF_UP != 0 {
				bytesIn += hdr.Data.Ibytes
				bytesOut += hdr.Data.Obytes
			}
		}
		data = data[msgLen:]
	}
	return bytesIn, bytesOut, nil
}
