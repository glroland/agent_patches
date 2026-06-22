package config

import (
	"fmt"
	"os"
	"path/filepath"

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
	`- run_diagnostic_command: use this for ALL read-only shell commands — ps, top, df, du, ` +
	`find, cat, grep, ss, netstat, journalctl, dmesg, systemctl status, apt-cache, rpm -q, ` +
	`and any other command that only reads or reports. These execute immediately with no ` +
	`operator involvement. This is the default tool for investigation.` + "\n" +
	`- run_approved_command: use this ONLY when you intend to change system state — ` +
	`installing or removing packages, starting/stopping/restarting services, deleting or ` +
	`overwriting files, modifying configuration. Never route an informational or read-only ` +
	`command through run_approved_command. If you find yourself writing a ps, df, cat, or ` +
	`grep command into run_approved_command, stop and use run_diagnostic_command instead.`

// Settings is the top-level configuration object loaded from the YAML file.
type Settings struct {
	Agent    AgentSettings    `yaml:"agent"`
	Logging  LoggingSettings  `yaml:"logging"`
	Tasks    TasksSettings    `yaml:"tasks"`
	Storage  StorageSettings  `yaml:"storage"`
	Server   ServerSettings   `yaml:"server"`
	Security SecuritySettings `yaml:"security"`
	Memory   MemorySettings   `yaml:"memory"`
	Loop     LoopSettings     `yaml:"loop"`

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

	return &s, nil
}

// FilePath returns the resolved config file path, exported for testing and diagnostics.
func FilePath() string {
	return filePath()
}

func filePath() string {
	if p := os.Getenv(EnvKey); p != "" {
		return p
	}
	return filepath.Join(".", defaultFile)
}
