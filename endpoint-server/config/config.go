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

// Settings is the top-level configuration object loaded from the YAML file.
type Settings struct {
	Agent        AgentSettings        `yaml:"agent"`
	Logging      LoggingSettings      `yaml:"logging"`
	Tasks        TasksSettings        `yaml:"tasks"`
	Storage      StorageSettings      `yaml:"storage"`
	Server       ServerSettings       `yaml:"server"`
	Security     SecuritySettings     `yaml:"security"`
	Notifier     NotifierSettings     `yaml:"notifier"`
	DailyTasks   DailyTasksSettings   `yaml:"daily_tasks"`
	LoginMonitor LoginMonitorSettings `yaml:"login_monitor"`
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
	Host      string `yaml:"host"`
	Port      int    `yaml:"port"`
	PublicURL string `yaml:"public_url"`
}

// SecuritySettings controls request authentication.
// Scheme must be "none" or "bearer". When "bearer", Token is the expected
// value of the Authorization: Bearer <token> header.
type SecuritySettings struct {
	Scheme string `yaml:"scheme"`
	Token  string `yaml:"token"`
}

// DailyTasksSettings controls the background maintenance loop.
type DailyTasksSettings struct {
	// WakeTime is the local wall-clock time (HH:MM) at which the loop fires
	// each day. Defaults to "00:00" (midnight) when unset.
	WakeTime   string             `yaml:"wake_time"`
	PatchCheck PatchCheckSettings `yaml:"patch_check"`
}

// PatchCheckSettings controls the scheduled patch-availability check.
type PatchCheckSettings struct {
	Enabled bool `yaml:"enabled"`
}

// LoginMonitorSettings controls the systemd-logind login monitor.
type LoginMonitorSettings struct {
	Enabled bool `yaml:"enabled"`
}

// NotifierSettings groups all event-sink configuration.
type NotifierSettings struct {
	Email EmailNotifierSettings `yaml:"email"`
}

// EmailNotifierSettings configures the SMTP email sink.
// TLSMode controls transport security:
//   - "starttls" (default) — plain TCP upgraded via STARTTLS; typical port 587
//   - "tls"                — implicit TLS from the start; typical port 465
//   - "none"               — no encryption; only suitable for local relay servers
type EmailNotifierSettings struct {
	Enabled  bool     `yaml:"enabled"`
	Host     string   `yaml:"host"`
	Port     int      `yaml:"port"`
	Username string   `yaml:"username"`
	Password string   `yaml:"password"`
	From     string   `yaml:"from"`
	To       []string `yaml:"to"`
	TLSMode  string   `yaml:"tls_mode"`
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
	if s.DailyTasks.WakeTime == "" {
		s.DailyTasks.WakeTime = "00:00"
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
