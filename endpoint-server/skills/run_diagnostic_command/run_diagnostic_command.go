// Package run_diagnostic_command provides a skill that executes read-only
// diagnostic shell commands immediately, without operator approval.
//
// Two enforcement layers prevent misuse:
//
//  1. Allowlist — the first token of the command must be a known-safe
//     diagnostic binary (ps, du, ss, journalctl, etc.).  Anything not on the
//     list is rejected.
//
//  2. Denylist — even for allowlisted binaries, the full command string is
//     scanned for patterns that indicate state modification: output redirects,
//     subshell execution, package-manager mutations, service state changes,
//     in-place edits, destructive find flags, and so on.
//
// A command that fails either check is rejected with an error message directing
// the caller to use run_approved_command instead.
package run_diagnostic_command

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"agent_patches/endpoint-server/a2a/tool"
)

const diagnosticTimeout = 30 * time.Second

type runDiagnosticInput struct {
	Command string `json:"command" jsonschema_description:"The read-only diagnostic shell command to execute (e.g. 'ps aux', 'df -h', 'ss -tunap', 'journalctl -n 50')."`
	Reason  string `json:"reason" jsonschema_description:"Brief explanation of what you are investigating and why this command answers it."`
}

// NewRunDiagnosticCommandTool returns a tool that runs read-only diagnostic
// shell commands immediately without operator approval.
//
// Use this tool for investigation and observation — reading process lists, disk
// usage, network connections, log output, package information, service status,
// etc.  Do NOT use it for commands that modify system state; use
// run_approved_command for anything that installs, removes, restarts, writes,
// or deletes.
func NewRunDiagnosticCommandTool() (tool.Tool, error) {
	return tool.New(
		"run_diagnostic_command",
		"Execute a read-only diagnostic shell command immediately, without operator approval. "+
			"Use for investigation only: process inspection (ps, top), disk usage (df, du, find), "+
			"network state (ss, ip, lsof), logs (journalctl, dmesg), package queries, "+
			"service status (systemctl status), and similar non-destructive observation. "+
			"For commands that modify system state (install, remove, restart, delete, write files), "+
			"use run_approved_command instead.",
		func(ctx context.Context, in runDiagnosticInput) (string, error) {
			cmd := strings.TrimSpace(in.Command)
			if cmd == "" {
				return "", fmt.Errorf("run_diagnostic_command: command must not be empty")
			}

			if err := validateCommand(cmd); err != nil {
				slog.Warn("run_diagnostic_command: command rejected", "command", cmd, "reason", err.Error())
				return "", err
			}

			slog.Info("run_diagnostic_command: executing", "command", cmd, "reason", in.Reason)
			cmdCtx, cancel := context.WithTimeout(ctx, diagnosticTimeout)
			defer cancel()

			out, execErr := exec.CommandContext(cmdCtx, "sh", "-c", cmd).CombinedOutput() //nolint:gosec
			output := strings.TrimRight(string(out), "\n")

			if execErr != nil {
				slog.Warn("run_diagnostic_command: command failed", "command", cmd, "error", execErr)
				if output != "" {
					return fmt.Sprintf("Command failed (%v):\n%s", execErr, output), nil
				}
				return fmt.Sprintf("Command failed: %v", execErr), nil
			}

			slog.Info("run_diagnostic_command: completed", "command", cmd, "output_len", len(output))
			if output == "" {
				return "Command completed with no output.", nil
			}
			return output, nil
		},
	)
}

// ValidateForTest exposes validateCommand for package-external tests.
func ValidateForTest(cmd string) error { return validateCommand(cmd) }

// validateCommand applies the two-layer safety check.
// Returns a non-nil error (with a message suitable for returning to the model)
// if the command is not permitted for unattended execution.
func validateCommand(cmd string) error {
	// Layer 1: allowlist — first token must be a known-safe diagnostic binary.
	first := firstToken(cmd)
	if !diagnosticAllowlist[first] {
		return fmt.Errorf(
			"run_diagnostic_command: %q is not on the diagnostic allowlist — "+
				"use run_approved_command so the operator can review it", first)
	}

	// Layer 2: denylist — scan the full command for dangerous patterns.
	for _, p := range denyPatterns {
		if p.re.MatchString(cmd) {
			return fmt.Errorf(
				"run_diagnostic_command: command matches forbidden pattern %q (%s) — "+
					"use run_approved_command so the operator can review it", p.re.String(), p.reason)
		}
	}

	return nil
}

// firstToken returns the first whitespace-delimited token of cmd, lower-cased.
func firstToken(cmd string) string {
	f := strings.Fields(cmd)
	if len(f) == 0 {
		return ""
	}
	return strings.ToLower(f[0])
}

// diagnosticAllowlist is the set of binaries permitted in run_diagnostic_command.
// Only the first token of the command is checked against this list.
var diagnosticAllowlist = map[string]bool{
	// Process inspection
	"ps": true, "top": true, "htop": true, "pgrep": true, "pstree": true,
	"pidstat": true,

	// Filesystem
	"df": true, "du": true, "ls": true, "find": true, "stat": true,
	"file": true, "lsblk": true, "blkid": true, "findmnt": true, "mount": true,

	// Text inspection and processing (write-mode flags caught by denylist)
	"grep": true, "egrep": true, "fgrep": true, "rgrep": true, "cat": true,
	"head": true, "tail": true, "less": true, "more": true,
	"awk": true, "sed": true, "sort": true, "uniq": true, "wc": true,
	"cut": true, "tr": true, "xargs": true, "column": true,

	// Network
	"ss": true, "netstat": true, "ip": true, "ifconfig": true, "lsof": true,
	"ping": true, "traceroute": true, "tracepath": true, "mtr": true,
	"nslookup": true, "dig": true, "host": true,
	"curl": true, "wget": true, // upload/POST modes caught by denylist

	// Logs
	"journalctl": true, "dmesg": true,

	// System info
	"uname": true, "hostname": true, "uptime": true, "free": true,
	"vmstat": true, "iostat": true, "mpstat": true, "sar": true, "dstat": true,
	"lscpu": true, "lspci": true, "lsusb": true, "dmidecode": true,
	"lsmod": true, "modinfo": true,

	// User / auth
	"who": true, "w": true, "last": true, "lastlog": true, "id": true,
	"getent": true,

	// Package info (install/remove/upgrade caught by denylist)
	"rpm": true, "dpkg": true, "apt-cache": true, "apt": true,
	"dnf": true, "yum": true,

	// Service status (start/stop/enable/disable caught by denylist)
	"systemctl": true, "service": true,

	// Miscellaneous safe builtins
	"date": true, "echo": true, "env": true, "printenv": true,
	"which": true, "whereis": true, "type": true,
}

// denyPattern pairs a compiled regex with a human-readable reason shown in
// the rejection error.
type denyPattern struct {
	re     *regexp.Regexp
	reason string
}

// denyPatterns is the ordered set of patterns that mark a command as
// state-modifying even when its leading binary is on the allowlist.
var denyPatterns = []denyPattern{
	// ── Output redirection ────────────────────────────────────────────────────
	{
		re:     regexp.MustCompile(`(?:^|[^<])(>{1,2})`),
		reason: "output redirect writes to a file",
	},

	// ── Subshell execution ────────────────────────────────────────────────────
	{
		re:     regexp.MustCompile(`\$\(`),
		reason: "subshell execution via $(…)",
	},
	{
		re:     regexp.MustCompile("`"),
		reason: "subshell execution via backtick",
	},

	// ── Pipe to a shell ───────────────────────────────────────────────────────
	{
		re:     regexp.MustCompile(`\|\s*(?:sh|bash|zsh|dash|ksh|csh|tcsh)\b`),
		reason: "pipe to a shell interpreter",
	},

	// ── find with execution or deletion flags ─────────────────────────────────
	{
		re:     regexp.MustCompile(`\bfind\b.+\s-(?:exec|execdir|delete)\b`),
		reason: "find -exec / -execdir / -delete modifies the filesystem",
	},

	// ── xargs chained to a destructive command ────────────────────────────────
	{
		re:     regexp.MustCompile(`\bxargs\b.+\b(?:rm|rmdir|unlink|shred|truncate)\b`),
		reason: "xargs piped to a destructive command",
	},

	// ── systemctl state-changing subcommands ──────────────────────────────────
	{
		re:     regexp.MustCompile(`\bsystemctl\b\s+(?:start|stop|restart|reload|enable|disable|mask|unmask|kill|reset-failed|set-property|edit)\b`),
		reason: "systemctl subcommand modifies service state",
	},

	// ── service state-changing subcommands ───────────────────────────────────
	{
		re:     regexp.MustCompile(`\bservice\b\s+\S+\s+(?:start|stop|restart|reload|force-reload)\b`),
		reason: "service command modifies service state",
	},

	// ── apt / apt-get ────────────────────────────────────────────────────────
	{
		re:     regexp.MustCompile(`\bapt(?:-get)?\b\s+(?:install|upgrade|dist-upgrade|full-upgrade|remove|purge|autoremove)\b`),
		reason: "apt/apt-get modifies installed packages",
	},

	// ── dnf / yum ────────────────────────────────────────────────────────────
	{
		re:     regexp.MustCompile(`\b(?:dnf|yum)\b\s+(?:install|update|upgrade|remove|erase|autoremove|swap|distro-sync)\b`),
		reason: "dnf/yum modifies installed packages",
	},

	// ── dpkg ─────────────────────────────────────────────────────────────────
	{
		re:     regexp.MustCompile(`\bdpkg\b\s+(?:-i|--install|-r|--remove|-P|--purge|--configure|--unpack)\b`),
		reason: "dpkg modifies installed packages",
	},

	// ── rpm ───────────────────────────────────────────────────────────────────
	{
		re:     regexp.MustCompile(`\brpm\b\s+(?:-i|-U|-F|-e|--install|--upgrade|--freshen|--erase)\b`),
		reason: "rpm modifies installed packages",
	},

	// ── sed in-place edit ────────────────────────────────────────────────────
	{
		re:     regexp.MustCompile(`\bsed\b\s+(?:\S+\s+)*-i\b`),
		reason: "sed -i edits files in place",
	},

	// ── curl write / mutation modes ──────────────────────────────────────────
	// -X with a mutation method
	{
		re:     regexp.MustCompile(`\bcurl\b.+-X\s*(?:POST|PUT|DELETE|PATCH)\b`),
		reason: "curl -X flag with mutation method",
	},
	// Long-form data/upload flags
	{
		re:     regexp.MustCompile(`\bcurl\b.+--(?:upload-file|data(?:-raw|-binary|-urlencode)?|json)\b`),
		reason: "curl long-form flag indicates a write or upload request",
	},
	// Short-form write flags: -d (data), -T (upload file), -F (form post)
	// Use .* (not .+) so the flag can appear immediately after "curl ".
	{
		re:     regexp.MustCompile(`\bcurl\b.*\s-[dTF][\s=]`),
		reason: "curl -d/-T/-F flag indicates a write or upload request",
	},

	// ── wget write modes ─────────────────────────────────────────────────────
	{
		re:     regexp.MustCompile(`\bwget\b.+(?:--(?:post-data|post-file|method=(?:POST|PUT|DELETE)))`),
		reason: "wget flag indicates a write request",
	},
}
