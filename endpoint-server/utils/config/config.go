package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

// EnvKey is the environment variable that overrides the config file path.
const EnvKey = "AGENT_PATCHES_CONFIG"

// defaultFile is resolved relative to the working directory when EnvKey is unset.
const defaultFile = "config.yaml"

// defaultResponsibilitySystemPrompt is the built-in fallback system prompt
// for responsibility runs, used only when ResponsibilitySystemPrompt is unset
// in the config file AND no OS-specific "<GOOS>-system-prompt.txt" file exists
// next to it. The canonical prompts live in config/linux-system-prompt.txt and
// config/windows-system-prompt.txt and are installed by deploy.sh.
const defaultResponsibilitySystemPrompt = `You are agent_patches, an AI system administrator. ` +
	`You are responsible for the health, security, and upkeep of this system. ` +
	`You will be given one specific responsibility to carry out using the tools ` +
	`available to you. Investigate thoroughly, take corrective action when it is ` +
	`safe and appropriate to do so, and report what you found and did clearly and ` +
	`concisely. If report_findings is available to you, call it whenever you ` +
	`observe something noteworthy, take an action, have a recommendation, or ` +
	`need operator approval before proceeding.` + "\n\n" +
	`TOOL SELECTION RULES (follow these exactly):` + "\n" +
	`- run_diagnostic_command: use this for ALL read-only shell or PowerShell commands. ` +
	`Linux examples: ps, top, df, du, ls, find, cat, grep, which, command -v, type, ` +
	`ss, netstat, journalctl, dmesg, systemctl status, apt-cache, rpm -q, ` +
	`docker ps/inspect/logs, podman ps/inspect/logs, kubectl get/describe/logs, ` +
	`virsh list/dominfo/domstats. ` +
	`Windows/PowerShell examples: Get-Process, Get-Service, Get-NetTCPConnection, ` +
	`Get-ChildItem, Get-CimInstance, Get-PSDrive, Get-EventLog, Get-NetAdapter, netstat, ` +
	`ipconfig, dir, and any powershell -Command "Get-..." or powershell -Command "Select-..." ` +
	`that only reads or reports. These execute immediately with no operator involvement. ` +
	`This is the default tool for investigation. A command is read-only based on what it ` +
	`does, not why you are running it — listing, querying, and reporting commands are ` +
	`ALWAYS run_diagnostic_command, even when the system has a critical problem or even ` +
	`when the purpose of the investigation is to identify candidates for future cleanup. ` +
	`Example: disk full at 100% — use run_diagnostic_command for ` +
	`"find / -xdev -type f -size +100M | sort -rn | head -20" to identify large files, ` +
	`then use run_approved_command only for the actual deletion such as "rm /var/log/old.log". ` +
	`The investigation step never requires approval regardless of how urgent the situation is.` + "\n" +
	`- run_approved_command: use this ONLY when you intend to change system state — ` +
	`installing or removing packages, starting/stopping/restarting services, deleting or ` +
	`overwriting files, modifying configuration, or running PowerShell cmdlets that write ` +
	`(Set-*, New-*, Remove-*, Start-Service, Stop-Service, Install-*, etc.). NEVER route ` +
	`a read-only command through run_approved_command — not even when you are assessing ` +
	`something in order to plan a cleanup or remediation. If you find yourself writing ` +
	`ps, df, du, ls, which, find, cat, grep, ss, netstat, Get-*, dir, where, or Select-* into ` +
	`run_approved_command, stop immediately and use run_diagnostic_command instead. ` +
	`If no corrective action is needed, do NOT call run_approved_command at all — simply ` +
	`write your conclusion in your response text or call report_findings. ` +
	`NEVER submit an approval request with a placeholder command such as "none", "n/a", ` +
	`"no action", or "no action required". If you have nothing to execute, say so in text. ` +
	`Approvals are asynchronous: run_approved_command returns immediately with a ` +
	`pending-approval confirmation and the command executes later, once the operator ` +
	`approves. Never wait for, poll, or re-submit a pending approval — state in your ` +
	`report that the remediation is pending operator approval and finish.` + "\n" +
	`- Do not run echo (or Write-Output / Write-Host) through any tool — both tools ` +
	`will reject it with an error. If you want to state a conclusion or confirm that ` +
	`a check passed, write it in your response text or call report_findings. ` +
	`Running echo via a command tool produces no useful information and wastes an approval slot.` + "\n\n" +
	`INCIDENT LEDGER (when manage_incidents is available):` + "\n" +
	`- The ledger records ongoing problems so they persist across runs. Any open ` +
	`incidents are appended to your instructions — treat them as already known.` + "\n" +
	`- When you find a persistent problem worth tracking (a filling disk, a runaway ` +
	`process, a failing drive), report it with a stable kebab-case fingerprint such as ` +
	`"disk-full-var" or "high-cpu-chrome". If the same underlying problem is already ` +
	`open, report against the existing fingerprint to record the recurrence — never ` +
	`open a duplicate incident or file a duplicate finding for it.` + "\n" +
	`- Log actions you take against an incident (action=log_action) and resolve ` +
	`incidents that are no longer occurring (action=resolve, with a resolution note). ` +
	`Do not open incidents for routine healthy check results.` + "\n\n" +
	`BASELINES (when compare_to_baseline or read_agent_memory is available):` + "\n" +
	`- Judge readings against this host's own history, not just fixed thresholds. ` +
	`Call compare_to_baseline with your skill's memory domain (e.g. check_drives, ` +
	`analyze_cpu_utilization, analyze_memory_utilization, analyze_network_utilization) ` +
	`to get the current snapshot plus ~1-hour, ~24-hour, and ~7-day-old baselines.` + "\n" +
	`- Report trends, not just levels: disk growth rate and predicted time-to-full, ` +
	`sustained versus momentary load, and readings far above the same time last week ` +
	`even when still below alert thresholds.` + "\n\n" +
	`STANDING POLICIES:` + "\n" +
	`- Some state-changing commands are pre-approved by operator-created standing ` +
	`policies; when a run_approved_command call matches one it executes immediately and ` +
	`the result says so. You cannot create or modify policies — only the operator can. ` +
	`Submit commands normally and let the tool decide.`

// Settings is the top-level configuration object loaded from the YAML file.
type Settings struct {
	Agent          AgentSettings          `yaml:"agent"`
	Logging        LoggingSettings        `yaml:"logging"`
	Tasks          TasksSettings          `yaml:"tasks"`
	Storage        StorageSettings        `yaml:"storage"`
	Server         ServerSettings         `yaml:"server"`
	Security       SecuritySettings       `yaml:"security"`
	Memory         MemorySettings         `yaml:"memory"`
	Loop           LoopSettings           `yaml:"loop"`
	LoginMonitor   LoginMonitorSettings   `yaml:"login_monitor"`
	NetworkMonitor NetworkMonitorSettings `yaml:"network_monitor"`
	Status         StatusSettings         `yaml:"status"`

	// Responsibilities is a dynamic list of recurring duties the agent should
	// carry out, each on its own schedule.
	Responsibilities []ResponsibilitySettings `yaml:"responsibilities"`

	// ResponsibilitySystemPrompt is the system prompt used for every
	// responsibility run; the responsibility's Instruction is sent as the
	// user prompt. When unset, it is loaded from "<GOOS>-system-prompt.txt"
	// in the config file's directory, falling back to
	// defaultResponsibilitySystemPrompt if that file is absent.
	ResponsibilitySystemPrompt string `yaml:"responsibility_system_prompt"`

	// SystemPurpose is a short operator-authored description of what this
	// host is for (e.g. "Primary database for internal apps"). It has no
	// YAML tag: the single source of truth is "purpose.txt" next to the
	// config file, deployed from the inventory CSV's optional "purpose"
	// column (see deploy/linux/deploy.sh). When non-empty it is folded into
	// both Agent.SystemPrompt and ResponsibilitySystemPrompt so every
	// interactive query and scheduled responsibility weighs it before
	// flagging normal purpose-serving activity as a problem.
	SystemPurpose string `yaml:"-"`
}

// ResponsibilitySettings describes one recurring duty assigned to the agent.
// Exactly one of Frequency or Time should be set to control scheduling:
//   - Frequency is a Go duration string (e.g. "1h", "30m") for recurring runs.
//   - Time is a local wall-clock time (HH:MM) for a once-daily run.
//
// Instruction is passed to the agent verbatim as the task prompt. Tools, when
// set, names the skills/tools the agent should have available while carrying
// out the instruction. WhenToNotify describes the conditions under which the
// agent should alert its manager (e.g. "on error", "always", "never").
type ResponsibilitySettings struct {
	Name         string   `yaml:"name"`
	Frequency    string   `yaml:"frequency,omitempty"`
	Time         string   `yaml:"time,omitempty"`
	Instruction  string   `yaml:"instruction"`
	Tools        []string `yaml:"tools,omitempty"`
	WhenToNotify string   `yaml:"when_to_notify"`
}

// LoginMonitorSettings controls the background login monitor and alerting.
type LoginMonitorSettings struct {
	// AllowedSources is a list of CIDRs or exact IP addresses from which remote
	// logins are expected. A remote login from an address outside this list
	// triggers a critical alert. When empty, no unusual-source alerting is done.
	AllowedSources []string `yaml:"allowed_sources"`

	// FailedLoginThreshold is the number of consecutive failed login attempts
	// from the same source IP that triggers a critical alert. Defaults to 3.
	FailedLoginThreshold int `yaml:"failed_login_threshold"`

	// DisableUnusualLoginBaseline turns off the self-learned baseline check —
	// flagging logins whose user, source, or time of day never appeared in
	// this host's own login history. Enabled by default.
	DisableUnusualLoginBaseline bool `yaml:"disable_unusual_login_baseline"`

	// BaselineMinEvents is the minimum number of prior login events a user
	// must have before their off-hours logins are evaluated. Defaults to 5.
	BaselineMinEvents int `yaml:"baseline_min_events"`
}

// NetworkMonitorSettings controls the background network connection monitor.
type NetworkMonitorSettings struct {
	// PollInterval is a Go duration string (e.g. "10s") for how often active
	// connections are sampled. Defaults to "10s" when unset.
	PollInterval string `yaml:"poll_interval"`

	// HistoryLimit caps the number of connection open/close/existing events
	// retained in history. Defaults to 2000 when unset or non-positive.
	HistoryLimit int `yaml:"history_limit"`

	// DisableUnusualConnectionBaseline turns off the history-based anomaly
	// check (new inbound port / new process / new remote host detection).
	// Enabled by default.
	DisableUnusualConnectionBaseline bool `yaml:"disable_unusual_connection_baseline"`
}

// LoopSettings controls the generic background wake-up loop.
type LoopSettings struct {
	// Heartbeat is a Go duration string (e.g. "30s", "5m") for how often the
	// loop wakes up. Defaults to "1s" when unset.
	Heartbeat string `yaml:"heartbeat"`
}

// AgentSettings controls OpenAI API behaviour.
type AgentSettings struct {
	Model        string `yaml:"model"`
	MaxTokens    int    `yaml:"max_tokens"`
	SystemPrompt string `yaml:"system_prompt"`
	MaxIter      int    `yaml:"max_iterations"`
	APIKey       string `yaml:"api_key"`
	BaseURL      string `yaml:"base_url"`
	// RequestTimeout is a Go duration string (e.g. "6m") for the per-request
	// HTTP timeout applied to each LLM API call. Defaults to "6m" — set it
	// just above the upstream gateway's GATEWAY_REQUEST_TIMEOUT so the gateway
	// can return a 504 before the client forcibly drops the connection.
	RequestTimeout string `yaml:"request_timeout"`
}

// LoggingSettings controls log verbosity and output destination.
type LoggingSettings struct {
	Level      string `yaml:"level"`
	File       string `yaml:"file"`
	MaxBackups int    `yaml:"max_backups"`
}

// TasksSettings lists which tasks the agent exposes.
type TasksSettings struct {
	Enabled []string `yaml:"enabled"`
}

// StorageSettings configures file-based task persistence.
type StorageSettings struct {
	TasksFile string `yaml:"tasks_file"`
}

// ServerSettings controls the HTTP listener.
type ServerSettings struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`

	// Name is the display label shown in gateway statistics. Defaults to the
	// system hostname when unset.
	Name string `yaml:"name"`

	// PublicURL is the URL embedded in the agent card. Optional: if unset,
	// it's derived from the host's FQDN (via reverse DNS) or hostname.
	PublicURL string `yaml:"public_url"`
}

// SecuritySettings controls request authentication.
// Scheme must be "none" or "bearer". When "bearer", Token is the expected
// value of the Authorization: Bearer <token> header.
type SecuritySettings struct {
	Scheme string `yaml:"scheme"`
	Token  string `yaml:"token"`
}

// MemorySettings configures the file-backed agent memory store.
type MemorySettings struct {
	// Root is the directory under which domain subdirs and attrs.json are stored.
	Root string `yaml:"root"`
}

// StatusSettings controls the status endpoint's AI summariser behaviour.
type StatusSettings struct {
	// SummaryTTL is a Go duration string (e.g. "1h") for the maximum time the
	// AI summary is cached before a forced refresh, even when alert content has
	// not changed. The summary is also refreshed immediately whenever the alert
	// content changes. Defaults to "5m".
	SummaryTTL string `yaml:"summary_ttl"`
}

// Load reads and parses the YAML config file. The file path is taken from the
// AGENT_PATCHES_CONFIG environment variable; when unset it falls back to
// ./config.yaml in the current working directory.
func Load() (*Settings, error) {
	path := filePath()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: reading %s: %w", path, err)
	}

	var s Settings
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("config: parsing %s: %w", path, err)
	}

	if s.Server.Host == "" {
		s.Server.Host = "0.0.0.0"
	}
	if s.Server.Name == "" {
		if h, err := os.Hostname(); err == nil {
			s.Server.Name = h
		}
	}
	if s.Server.Port == 0 {
		s.Server.Port = 8080
	}
	if s.Security.Scheme == "" {
		s.Security.Scheme = "none"
	}
	if s.Memory.Root == "" {
		s.Memory.Root = "./agent_memory"
	}
	if s.Loop.Heartbeat == "" {
		s.Loop.Heartbeat = "1s"
	}
	if s.Logging.MaxBackups == 0 {
		s.Logging.MaxBackups = 10
	}
	if s.Agent.RequestTimeout == "" {
		s.Agent.RequestTimeout = "6m"
	}
	if s.ResponsibilitySystemPrompt == "" {
		prompt, err := loadOSSystemPrompt(path)
		if err != nil {
			slog.Warn("config: could not load OS system prompt file, using built-in default",
				"file", OSSystemPromptPath(path), "error", err)
		}
		if prompt == "" {
			prompt = defaultResponsibilitySystemPrompt
		}
		s.ResponsibilitySystemPrompt = prompt
	}
	purpose, err := loadPurpose(path)
	if err != nil {
		slog.Warn("config: could not load purpose file, continuing without a system purpose",
			"file", PurposePath(path), "error", err)
	}
	if purpose != "" {
		s.SystemPurpose = purpose
		block := purposePromptBlock(purpose)
		s.Agent.SystemPrompt = s.Agent.SystemPrompt + block
		s.ResponsibilitySystemPrompt = s.ResponsibilitySystemPrompt + block
		slog.Info("config: loaded system purpose, folded into agent and responsibility system prompts",
			"file", PurposePath(path), "purpose", purpose)
	}
	if s.LoginMonitor.FailedLoginThreshold <= 0 {
		s.LoginMonitor.FailedLoginThreshold = 3
	}
	if s.LoginMonitor.BaselineMinEvents <= 0 {
		s.LoginMonitor.BaselineMinEvents = 5
	}
	if s.Status.SummaryTTL == "" {
		s.Status.SummaryTTL = "5m"
	}

	osResps, err := loadOSResponsibilities(path)
	if err != nil {
		slog.Warn("config: could not load OS responsibilities file, using main config only",
			"file", OSResponsibilitiesPath(path), "error", err)
	}
	s.Responsibilities = mergeResponsibilities(osResps, s.Responsibilities)

	return &s, nil
}

// FilePath returns the resolved config file path, exported for testing and diagnostics.
func FilePath() string {
	return filePath()
}

// OSResponsibilitiesPath returns the path of the OS-specific responsibilities
// file that is loaded alongside the given config file path. The file is named
// "<runtime.GOOS>-responsibilities.yaml" and lives in the same directory.
func OSResponsibilitiesPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), runtime.GOOS+"-responsibilities.yaml")
}

// OSSystemPromptPath returns the path of the OS-specific responsibility
// system prompt file that is loaded alongside the given config file path.
// The file is named "<runtime.GOOS>-system-prompt.txt" and lives in the
// same directory.
func OSSystemPromptPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), runtime.GOOS+"-system-prompt.txt")
}

// PurposePath returns the path of the optional system purpose file that is
// loaded alongside the given config file path. Unlike the OS-specific prompt
// and responsibilities files, this file is named "purpose.txt" — a host's
// purpose is business context, not an OS-dependent instruction set.
func PurposePath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "purpose.txt")
}

// loadPurpose reads the optional system purpose file. Returns "", nil when
// the file does not exist or is empty — the file is entirely optional and
// there is no fallback default (there is nothing sensible to default a
// host's purpose to).
func loadPurpose(configPath string) (string, error) {
	path := PurposePath(configPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// purposePromptBlock renders the system purpose as a system-prompt fragment
// instructing the agent to weigh it before judging severity or recommending
// remediation. Appended to both Agent.SystemPrompt and
// ResponsibilitySystemPrompt so it reaches every skill and every scheduled
// responsibility.
func purposePromptBlock(purpose string) string {
	return "\n\nSystem purpose: " + purpose + "\n" +
		`Weigh this stated purpose when judging severity and remediation: normal ` +
		`resource usage, running processes, or open connections that serve this ` +
		`purpose are not incidents on their own. Do not recommend stopping, ` +
		`disabling, or removing a service that is core to this system's purpose ` +
		`in order to free resources — flag it only if it is genuinely malfunctioning.`
}

// loadOSSystemPrompt reads the OS-specific responsibility system prompt file.
// Returns "", nil when the file does not exist or is empty (not an error —
// the file is optional and the built-in default applies).
func loadOSSystemPrompt(configPath string) (string, error) {
	path := OSSystemPromptPath(configPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Info("config: no OS system prompt file found, using built-in default", "path", path)
			return "", nil
		}
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	prompt := strings.TrimSpace(string(data))
	if prompt == "" {
		slog.Warn("config: OS system prompt file is empty, using built-in default", "path", path)
		return "", nil
	}
	slog.Info("config: loaded OS responsibility system prompt", "file", path, "bytes", len(prompt))
	return prompt, nil
}

// osResponsibilitiesFile is the YAML structure of a standalone responsibilities file.
type osResponsibilitiesFile struct {
	Responsibilities []ResponsibilitySettings `yaml:"responsibilities"`
}

// loadOSResponsibilities reads the OS-specific responsibilities file. Returns
// nil, nil when the file does not exist (not an error — file is optional).
func loadOSResponsibilities(configPath string) ([]ResponsibilitySettings, error) {
	path := OSResponsibilitiesPath(configPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Info("config: no OS responsibilities file found, using main config only", "path", path)
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var f osResponsibilitiesFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	slog.Info("config: loaded OS responsibilities", "file", path, "count", len(f.Responsibilities))
	return f.Responsibilities, nil
}

// mergeResponsibilities merges OS-default responsibilities with instance-level
// overrides. Entries from override are always included; entries from base are
// included only when no override entry shares the same name.
func mergeResponsibilities(base, override []ResponsibilitySettings) []ResponsibilitySettings {
	if len(base) == 0 {
		return override
	}
	overrideNames := make(map[string]bool, len(override))
	for _, r := range override {
		overrideNames[r.Name] = true
	}
	result := make([]ResponsibilitySettings, 0, len(base)+len(override))
	for _, r := range base {
		if !overrideNames[r.Name] {
			result = append(result, r)
		}
	}
	return append(result, override...)
}

func filePath() string {
	if p := os.Getenv(EnvKey); p != "" {
		return p
	}
	return filepath.Join(".", defaultFile)
}
