//go:build darwin

package connmonitor

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// gatherTimeout bounds the lsof invocation used on each poll.
const gatherTimeout = 15 * time.Second

// snapshot returns all active TCP/UDP connections using lsof, which (unlike
// netstat on macOS) reports the owning process without elevated privileges
// for the invoking user's own sockets.
func snapshot(ctx context.Context) ([]Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, gatherTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP", "-iUDP").Output()
	if err != nil {
		return nil, fmt.Errorf("connmonitor: lsof: %w", err)
	}
	return parseLsofConns(string(out)), nil
}

// parseLsofConns parses `lsof -nP -iTCP -iUDP` output. Example lines:
//
//	sshd    812 root 3u IPv4 0x0 0t0  TCP 192.168.1.5:22->192.168.1.10:54321 (ESTABLISHED)
//	sshd    812 root 4u IPv4 0x0 0t0  TCP *:22 (LISTEN)
//
// Only rows whose NAME column contains "->" (an actual peer) are kept;
// listening sockets and unconnected UDP sockets have no peer and are skipped.
func parseLsofConns(out string) []Conn {
	var conns []Conn
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 9 || fields[0] == "COMMAND" {
			continue
		}
		proto := strings.ToLower(fields[7])
		if proto != "tcp" && proto != "udp" {
			continue
		}
		name := fields[8]
		if !strings.Contains(name, "->") {
			continue
		}
		parts := strings.SplitN(name, "->", 2)
		localAddr, localPort := splitHostPort(parts[0])
		remoteAddr, remotePort := splitHostPort(parts[1])

		state := ""
		if len(fields) > 9 {
			state = strings.Trim(fields[9], "()")
		}

		conns = append(conns, Conn{
			Proto:      proto,
			LocalAddr:  localAddr,
			LocalPort:  localPort,
			RemoteAddr: remoteAddr,
			RemotePort: remotePort,
			State:      state,
			PID:        fields[1],
			Process:    fields[0],
		})
	}
	return conns
}
