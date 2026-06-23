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
			"Use for investigation only: process inspection (ps, top, tasklist, Get-Process), "+
			"disk and storage (df, du, lsblk, smartctl, Get-PSDrive, Get-PhysicalDisk, Get-Disk, Get-Volume), "+
			"network state (ss, ip, lsof, netstat, Get-NetTCPConnection, netsh, ipconfig), "+
			"memory (free, vmstat, Get-CimInstance Win32_OperatingSystem), "+
			"containers (docker ps, docker inspect, docker logs, docker stats, "+
			"podman ps, podman inspect, podman logs), "+
			"logs (journalctl, dmesg, Get-EventLog, wevtutil), "+
			"package queries, service status (systemctl status, Get-Service), "+
			"and similar non-destructive observation. "+
			"On Windows, bare PowerShell Verb-Noun cmdlets may be used WITHOUT the "+
			"'powershell -Command' wrapper — e.g. "+
			"'Get-Process | Sort-Object CPU -Descending | Select-Object -First 5' "+
			"or 'Get-PhysicalDisk | Select-Object FriendlyName, HealthStatus' are both valid. "+
			"Any PowerShell cmdlet starting with Get-, Measure-, Select-, Sort-, Format-, "+
			"Where-, ForEach-, Compare-, Find-, Resolve-, or Test- is permitted without approval. "+
			"For commands that modify system state (install, remove, restart, delete, write files, "+
			"Set-*, Remove-*, New-Item, Start/Stop-Service, smartctl -t/-s), "+
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

			out, execErr := runShell(ctx, cmd, diagnosticTimeout)
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
	first := firstToken(cmd)

	// Pre-check: echo (and PowerShell equivalents) are never diagnostic commands.
	// Plain echo produces no useful information; redirect/pipe forms are
	// state-modifying and belong in run_approved_command.
	if first == "echo" || first == "write-output" || first == "write-host" {
		return fmt.Errorf(
			"run_diagnostic_command: %q is not a diagnostic command — "+
				"write the message in your response text or call report_findings instead", first)
	}

	// Layer 1: allowlist — first token must be a known-safe diagnostic binary
	// OR a bare PowerShell read-only Verb-Noun cmdlet (e.g. "Get-Process").
	if !diagnosticAllowlist[first] && !isPowerShellReadOnlyCmdlet(first) {
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

// isPowerShellReadOnlyCmdlet reports whether token is a bare PowerShell
// Verb-Noun cmdlet whose verb is known to be read-only. This lets the agent
// issue commands like "Get-Process | Sort-Object CPU" without the
// "powershell -Command" prefix, which the model sometimes omits.
// The deny-pattern layer still applies to the full command string, so
// any state-modifying argument (Out-File, Set-Content, etc.) is still caught.
func isPowerShellReadOnlyCmdlet(token string) bool {
	// Must be Verb-Noun form: at least one letter, a hyphen, at least one letter.
	idx := strings.Index(token, "-")
	if idx < 1 || idx == len(token)-1 {
		return false
	}
	verb := token[:idx]
	// Read-only PowerShell verbs by convention.
	switch verb {
	case "get", "measure", "select", "sort", "format", "compare",
		"find", "resolve", "test", "where", "foreach", "show", "read",
		"search", "convertto", "convertfrom", "out", "tee":
		return true
	}
	return false
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

	// Container runtimes (state-modifying subcommands caught by denylist)
	"docker": true, "podman": true, "podman-compose": true, "docker-compose": true,
	"nerdctl": true, "crictl": true,

	// Kubernetes / orchestration (state-modifying subcommands caught by denylist)
	"kubectl": true, "helm": true,

	// Virtualisation (state-modifying subcommands caught by denylist)
	"virsh": true,

	// Storage / SMART
	"smartctl": true, "hdparm": true, "nvme": true,

	// NFS / filesystem tools
	"nfsstat": true, "nfsmount": true, "showmount": true,
	"mountstats": true,

	// Miscellaneous safe builtins
	// Note: "echo" is intentionally absent — plain echo is never a diagnostic
	// command and is rejected with a specific error before the allowlist check.
	"date": true, "env": true, "printenv": true,
	"which": true, "whereis": true, "type": true,

	// Windows — PowerShell and cmd.exe for read-only diagnostics
	// State-modifying cmdlets are caught by the PowerShell deny patterns below.
	"powershell": true, "powershell.exe": true,
	"cmd": true, "cmd.exe": true,
	"wmic": true, "tasklist": true, "netsh": true,
	"ipconfig": true, "systeminfo": true, "sc": true,
	"wevtutil": true, "qwinsta": true, "query": true,
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
	// Digits before > are file-descriptor redirections (2>/dev/null, 2>&1)
	// which are harmless; only flag > not preceded by a digit or <.
	{
		re:     regexp.MustCompile(`(?:^|[^<0-9])(>{1,2})`),
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

	// ── docker / podman / nerdctl / crictl state-modifying subcommands ────────
	{
		re: regexp.MustCompile(
			`\b(?:docker|podman|nerdctl|crictl)\s+` +
				`(?:run|create|start|stop|restart|kill|pause|unpause|` +
				`rm|rmi|exec|build|commit|tag|push|` +
				`cp|rename|update|login|logout|` +
				`system\s+prune|container\s+prune|` +
				`image\s+(?:rm|prune|build|push|import|load)|` +
				`network\s+(?:create|rm|connect|disconnect|prune)|` +
				`volume\s+(?:create|rm|prune))\b`,
		),
		reason: "docker/podman subcommand modifies container or image state",
	},
	// docker-compose / podman-compose lifecycle commands
	{
		re: regexp.MustCompile(
			`\b(?:docker-compose|podman-compose)\s+` +
				`(?:up|down|start|stop|restart|kill|rm|build|push|pull|create|run|exec)\b`,
		),
		reason: "docker-compose/podman-compose subcommand modifies service state",
	},

	// ── kubectl state-modifying subcommands ───────────────────────────────────
	{
		re: regexp.MustCompile(
			`\bkubectl\s+` +
				`(?:apply|create|delete|replace|patch|edit|label|annotate|` +
				`scale|taint|drain|cordon|uncordon|exec|cp|` +
				`rollout\s+(?:restart|undo)|` +
				`set\s+(?:image|env|resources|selector|serviceaccount))\b`,
		),
		reason: "kubectl subcommand modifies cluster state",
	},
	// helm state-modifying subcommands
	{
		re: regexp.MustCompile(
			`\bhelm\s+(?:install|upgrade|uninstall|rollback|repo\s+add|repo\s+remove|push|package)\b`,
		),
		reason: "helm subcommand modifies release state",
	},

	// ── PowerShell filesystem / content write cmdlets ─────────────────────────
	{
		re:     regexp.MustCompile(`(?i)\b(?:Set|Remove|New|Add|Clear|Copy|Move|Rename)-(?:Item|Content|Property|Location|Acl)\b`),
		reason: "PowerShell cmdlet modifies filesystem or registry state",
	},
	// ── PowerShell service / process lifecycle ─────────────────────────────────
	{
		re:     regexp.MustCompile(`(?i)\b(?:Start|Stop|Restart|Suspend|Resume)-(?:Service|Process)\b`),
		reason: "PowerShell cmdlet modifies service or process state",
	},
	// ── PowerShell package / feature management ────────────────────────────────
	{
		re:     regexp.MustCompile(`(?i)\b(?:Install|Uninstall)-(?:Module|Package|WindowsFeature|WindowsOptionalFeature|Script)\b`),
		reason: "PowerShell cmdlet modifies installed packages or features",
	},
	// ── PowerShell output to file ──────────────────────────────────────────────
	{
		re:     regexp.MustCompile(`(?i)\bOut-File\b`),
		reason: "Out-File writes to a file",
	},
	// ── PowerShell arbitrary execution ────────────────────────────────────────
	{
		re:     regexp.MustCompile(`(?i)\b(?:Invoke-Expression|iex)\b`),
		reason: "Invoke-Expression executes arbitrary code",
	},
	// ── PowerShell execution policy change ────────────────────────────────────
	{
		re:     regexp.MustCompile(`(?i)\bSet-ExecutionPolicy\b`),
		reason: "Set-ExecutionPolicy changes the PowerShell execution policy",
	},
	// ── Windows sc.exe state-modifying subcommands ────────────────────────────
	{
		re:     regexp.MustCompile(`\bsc(?:\.exe)?\s+(?:start|stop|create|delete|config|failure|pause|continue)\b`),
		reason: "sc.exe subcommand modifies service state",
	},

	// ── smartctl test / settings flags ───────────────────────────────────────
	// -t runs self-tests; -s enables/disables SMART or offline testing;
	// -o/-S controls offline data collection / attribute autosave.
	{
		re:     regexp.MustCompile(`\bsmartctl\b.*\s-[tsToS]\b`),
		reason: "smartctl flag triggers a drive self-test or changes SMART settings",
	},

	// ── virsh state-modifying subcommands ─────────────────────────────────────
	{
		re: regexp.MustCompile(
			`\bvirsh\s+` +
				`(?:start|shutdown|destroy|reboot|reset|suspend|resume|` +
				`create|define|undefine|migrate|` +
				`attach-(?:device|disk|interface)|detach-(?:device|disk|interface)|` +
				`snapshot-create|snapshot-delete|snapshot-revert|` +
				`vol-create|vol-delete|vol-clone|` +
				`net-start|net-destroy|net-define|net-undefine|` +
				`pool-start|pool-destroy|pool-define|pool-undefine)\b`,
		),
		reason: "virsh subcommand modifies VM or network state",
	},
}
