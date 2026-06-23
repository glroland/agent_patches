package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"
)

// EnvKey is the environment variable that overrides the config file path.
const EnvKey = "AGENT_PATCHES_CONFIG"

// defaultFile is resolved relative to the working directory when EnvKey is unset.
const defaultFile = "config.yaml"

// defaultResponsibilitySystemPrompt is used as the system prompt for every
// responsibility run when ResponsibilitySystemPrompt is unset.
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
	`Linux examples: ps, top, df, du, find, cat, grep, ss, netstat, journalctl, dmesg, ` +
	`systemctl status, apt-cache, rpm -q, docker ps/inspect/logs, podman ps/inspect/logs, ` +
	`kubectl get/describe/logs, virsh list/dominfo/domstats. ` +
	`Windows/PowerShell examples: Get-Process, Get-Service, Get-NetTCPConnection, ` +
	`Get-ChildItem, Get-CimInstance, Get-PSDrive, Get-EventLog, Get-NetAdapter, netstat, ` +
	`ipconfig, and any powershell -Command "Get-..." or powershell -Command "Select-..." ` +
	`that only reads or reports. These execute immediately with no operator involvement. ` +
	`This is the default tool for investigation. A command is read-only based on what it ` +
	`does, not why you are running it — listing, querying, and reporting commands are ` +
	`ALWAYS run_diagnostic_command, even when the system has a critical problem. ` +
	`Example: disk full at 100% — use run_diagnostic_command for ` +
	`"find / -xdev -type f -size +100M | sort -rn | head -20" to identify large files, ` +
	`then use run_approved_command only for the actual deletion such as "rm /var/log/old.log". ` +
	`The investigation step never requires approval regardless of how urgent the situation is.` + "\n" +
	`- run_approved_command: use this ONLY when you intend to change system state — ` +
	`installing or removing packages, starting/stopping/restarting services, deleting or ` +
	`overwriting files, modifying configuration, or running PowerShell cmdlets that write ` +
	`(Set-*, New-*, Remove-*, Start-Service, Stop-Service, Install-*, etc.). Never route ` +
	`an informational or read-only command through run_approved_command. If you find ` +
	`yourself writing a ps, df, du, cat, find, grep, Get-*, or Select-* command into ` +
	`run_approved_command, stop and use run_diagnostic_command instead. ` +
	`If no corrective action is needed, do NOT call run_approved_command at all — simply ` +
	`write your conclusion in your response text or call report_findings. Never submit a ` +
	`"no action required" or "none" approval request; that wastes an operator approval slot.` + "\n" +
	`- Do not run echo commands through any tool. If you want to state a conclusion or ` +
	`confirm that a check passed, write it in your response text or call report_findings. ` +
	`Running echo via a command tool produces no useful information and wastes an approval slot.`

// Settings is the top-level configuration object loaded from the YAML file.
type Settings struct {
	Agent        AgentSettings        `yaml:"agent"`
	Logging      LoggingSettings      `yaml:"logging"`
	Tasks        TasksSettings        `yaml:"tasks"`
	Storage      StorageSettings      `yaml:"storage"`
	Server       ServerSettings       `yaml:"server"`
	Security     SecuritySettings     `yaml:"security"`
	Memory       MemorySettings       `yaml:"memory"`
	Loop         LoopSettings         `yaml:"loop"`
	LoginMonitor LoginMonitorSettings `yaml:"login_monitor"`

	// Responsibilities is a dynamic list of recurring duties the agent should
	// carry out, each on its own schedule.
	Responsibilities []ResponsibilitySettings `yaml:"responsibilities"`

	// ResponsibilitySystemPrompt is the system prompt used for every
	// responsibility run; the responsibility's Instruction is sent as the
	// user prompt. Defaults to defaultResponsibilitySystemPrompt when unset.
	ResponsibilitySystemPrompt string `yaml:"responsibility_system_prompt"`
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
}

// LoggingSettings controls log verbosity and output destination.
type LoggingSettings struct {
	Level string `yaml:"level"`
	File  string `yaml:"file"`
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
	if s.ResponsibilitySystemPrompt == "" {
		s.ResponsibilitySystemPrompt = defaultResponsibilitySystemPrompt
	}
	if s.LoginMonitor.FailedLoginThreshold <= 0 {
		s.LoginMonitor.FailedLoginThreshold = 3
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
