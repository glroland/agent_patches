package loginmonitor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skillstate"
	"agent_patches/endpoint-server/utils/config"
	"agent_patches/endpoint-server/utils/notifier"
)

// sshd log patterns for failed authentication attempts.
var (
	// "Failed password for [invalid user] <user> from <ip> port ..."
	failedPassRe = regexp.MustCompile(`Failed password for (?:invalid user )?(\S+) from (\S+)`)
	// "Invalid user <user> from <ip>"
	invalidUserRe = regexp.MustCompile(`Invalid user (\S+) from (\S+)`)
	// "Connection closed by invalid user <user> <ip> port ..."
	closedByInvalidRe = regexp.MustCompile(`Connection closed by invalid user (\S+) (\S+)`)
)

// journaldEntry is the relevant subset of journald's --output json format.
type journaldEntry struct {
	Message string `json:"MESSAGE"`
}

// FailedMonitor tails the system journal for sshd authentication failures,
// records them in attrs storage, and fires critical alerts when a source IP
// exceeds the configured consecutive-failure threshold.
type FailedMonitor struct {
	mem              *memory.Store
	notify           *notifier.Notifier
	cfg              config.LoginMonitorSettings
	consecutiveFails map[string]int // keyed by source IP (or raw host if IP unknown)
}

// NewFailedMonitor creates a FailedMonitor. Call Start to launch it.
func NewFailedMonitor(mem *memory.Store, notify *notifier.Notifier, cfg config.LoginMonitorSettings) *FailedMonitor {
	return &FailedMonitor{
		mem:              mem,
		notify:           notify,
		cfg:              cfg,
		consecutiveFails: make(map[string]int),
	}
}

// Start launches the background goroutine. It returns immediately and exits
// silently if journalctl is not available (macOS, Windows).
func (m *FailedMonitor) Start(ctx context.Context) {
	if _, err := exec.LookPath("journalctl"); err != nil {
		slog.Info("failedloginmonitor: journalctl not found, failed login history disabled")
		return
	}
	slog.Info("failedloginmonitor: starting", "threshold", m.cfg.FailedLoginThreshold)
	go m.runWithRetry(ctx)
}

func (m *FailedMonitor) runWithRetry(ctx context.Context) {
	for {
		if err := m.run(ctx); err != nil {
			slog.Warn("failedloginmonitor: watch loop exited, retrying", "error", err, "delay", retryDelay)
		}
		select {
		case <-ctx.Done():
			slog.Info("failedloginmonitor: stopped")
			return
		case <-time.After(retryDelay):
		}
	}
}

// run streams journald output for sshd units and handles each line.
func (m *FailedMonitor) run(ctx context.Context) error {
	// -n 0: no historical lines; only follow new entries.
	cmd := exec.CommandContext(ctx, "journalctl",
		"--follow", "--output", "json",
		"-u", "sshd", "-u", "ssh",
		"-n", "0",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start journalctl: %w", err)
	}
	defer cmd.Wait() //nolint:errcheck

	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		var entry journaldEntry
		if err := json.Unmarshal(sc.Bytes(), &entry); err != nil {
			continue
		}
		if entry.Message != "" {
			m.handleMessage(entry.Message)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read stdout: %w", err)
	}
	return fmt.Errorf("journalctl exited unexpectedly")
}

// handleMessage parses one journal line, records the failure, and checks thresholds.
func (m *FailedMonitor) handleMessage(msg string) {
	username, rawHost, reason := parseFailureMessage(msg)
	if username == "" {
		return
	}

	ip, hostname := enrichSourceInfo(rawHost)
	ev := FailedLoginEvent{
		Username:         username,
		SourceIP:         ip,
		RemoteHost:       rawHost,
		ResolvedHostname: hostname,
		Timestamp:        time.Now().UTC(),
		Service:          "sshd",
		Reason:           reason,
	}
	m.appendFailedEvent(ev)
	slog.Info("failedloginmonitor: failed login recorded", "user", username, "source", rawHost, "reason", reason)

	key := ip
	if key == "" {
		key = rawHost
	}
	if key == "" {
		return
	}

	// Reset counter if a successful login from this source has been recorded
	// since the last failure, then increment for the current failure.
	m.resetIfSucceeded(key, ip)
	m.consecutiveFails[key]++
	count := m.consecutiveFails[key]

	threshold := m.cfg.FailedLoginThreshold
	// Alert at threshold, then again at each subsequent multiple.
	if threshold > 0 && count >= threshold && (count-threshold)%threshold == 0 {
		subject := fmt.Sprintf("[CRITICAL] %d consecutive failed logins from %s", count, key)
		body := fmt.Sprintf(
			"Consecutive failed login attempts detected.\n\nSource IP:  %s\nHostname:   %s\nAttempts:   %d\nLast user:  %s\nReason:     %s\nTime:       %s",
			ip, hostname, count, username, reason, ev.Timestamp.Format(time.RFC1123),
		)
		m.notify.Notify(context.Background(), subject, body)
		_ = skillstate.Save(m.mem, "check_interactive_logins", skillstate.HealthCritical,
			fmt.Sprintf("brute force: %d consecutive failed logins from %s", count, key))
		slog.Warn("failedloginmonitor: consecutive failure threshold reached", "source", key, "count", count)
	}
}

// resetIfSucceeded zeroes the consecutive-failure counter for key if the
// successful login history contains an EventLogin from sourceIP more recent
// than the last recorded failure from that source. We scan at most the last
// 100 successful events to keep this lightweight.
func (m *FailedMonitor) resetIfSucceeded(key, sourceIP string) {
	if sourceIP == "" {
		return
	}
	history, err := ReadHistory(m.mem)
	if err != nil || len(history) == 0 {
		return
	}
	start := 0
	if len(history) > 100 {
		start = len(history) - 100
	}
	for i := len(history) - 1; i >= start; i-- {
		ev := history[i]
		if ev.EventType == EventLogin && ev.SourceIP == sourceIP {
			delete(m.consecutiveFails, key)
			return
		}
	}
}

// appendFailedEvent appends ev to the failed login history, capping at maxFailedHistory.
func (m *FailedMonitor) appendFailedEvent(ev FailedLoginEvent) {
	history, _ := ReadFailedHistory(m.mem)
	history = append(history, ev)
	if len(history) > maxFailedHistory {
		history = history[len(history)-maxFailedHistory:]
	}
	if err := m.mem.Attrs().Set(failedHistoryKey, history); err != nil {
		slog.Warn("failedloginmonitor: failed to write history", "error", err)
	}
}

// parseFailureMessage extracts (username, sourceHost, reason) from a known
// sshd log message. Returns empty strings when the message is not a failure.
func parseFailureMessage(msg string) (username, rawHost, reason string) {
	if m := failedPassRe.FindStringSubmatch(msg); len(m) >= 3 {
		reason = "invalid password"
		if strings.Contains(msg, "invalid user") {
			reason = "invalid user"
		}
		return m[1], m[2], reason
	}
	if m := invalidUserRe.FindStringSubmatch(msg); len(m) >= 3 {
		return m[1], m[2], "invalid user"
	}
	if m := closedByInvalidRe.FindStringSubmatch(msg); len(m) >= 3 {
		return m[1], m[2], "connection closed by invalid user"
	}
	return "", "", ""
}
