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
	Agent           AgentSettings           `yaml:"agent"`
	Logging         LoggingSettings         `yaml:"logging"`
	Tasks           TasksSettings           `yaml:"tasks"`
	Storage         StorageSettings         `yaml:"storage"`
	Server          ServerSettings          `yaml:"server"`
	Security        SecuritySettings        `yaml:"security"`
	Notifier        NotifierSettings        `yaml:"notifier"`
	DailyTasks      DailyTasksSettings      `yaml:"daily_tasks"`
	LoginMonitor    LoginMonitorSettings    `yaml:"login_monitor"`
	DiskMonitor     DiskMonitorSettings     `yaml:"disk_monitor"`
	MemoryMonitor   MemoryMonitorSettings   `yaml:"memory_monitor"`
	NetworkUpload   NetworkUploadSettings   `yaml:"network_upload_monitor"`
	NetworkDownload NetworkDownloadSettings `yaml:"network_download_monitor"`
	Memory          MemorySettings          `yaml:"memory"`
	AISysAdmin      AISysAdminSettings      `yaml:"ai_sysadmin"`
	Loop            LoopSettings            `yaml:"loop"`
}

// LoopSettings controls the generic background wake-up loop.
type LoopSettings struct {
	// Interval is a Go duration string (e.g. "30s", "5m") for how often the
	// loop wakes up. Defaults to "60s" when unset.
	Interval string `yaml:"interval"`
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

// NetworkUploadSettings controls the upload-rate monitor.
type NetworkUploadSettings struct {
	Enabled       bool    `yaml:"enabled"`
	ThresholdMBps float64 `yaml:"threshold_mbps"`
	// Interval is a Go duration string (e.g. "1m", "30s") for how often to sample.
	Interval string `yaml:"interval"`
}

// NetworkDownloadSettings controls the download-rate monitor.
type NetworkDownloadSettings struct {
	Enabled       bool    `yaml:"enabled"`
	ThresholdMBps float64 `yaml:"threshold_mbps"`
	Interval      string  `yaml:"interval"`
}

// MemoryMonitorSettings controls the periodic memory usage monitor.
type MemoryMonitorSettings struct {
	Enabled              bool    `yaml:"enabled"`
	ThresholdPercent     float64 `yaml:"threshold_percent"`
	SwapThresholdPercent float64 `yaml:"swap_threshold_percent"`
	// Interval is a Go duration string (e.g. "5m", "1h") for how often to check.
	Interval string `yaml:"interval"`
}

// DiskMonitorSettings controls the periodic disk space monitor.
type DiskMonitorSettings struct {
	Enabled          bool    `yaml:"enabled"`
	ThresholdPercent float64 `yaml:"threshold_percent"`
	// Interval is a Go duration string (e.g. "1h", "30m") for how often to check.
	Interval string `yaml:"interval"`
}

// NotifierSettings groups all event-sink configuration.
type NotifierSettings struct {
	Email EmailNotifierSettings `yaml:"email"`
}

// AISysAdminSettings controls the background AI sysadmin agent.
type AISysAdminSettings struct {
	Enabled bool `yaml:"enabled"`
	// Interval is a Go duration string for how often the analysis cycle runs.
	Interval string `yaml:"interval"`
	// Model overrides agent.model for the sysadmin cycle when set.
	Model string `yaml:"model"`
}

// MemorySettings configures the file-backed agent memory store.
type MemorySettings struct {
	// Root is the directory under which domain subdirs and attrs.json are stored.
	Root string `yaml:"root"`
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
	if s.DiskMonitor.ThresholdPercent == 0 {
		s.DiskMonitor.ThresholdPercent = 85
	}
	if s.DiskMonitor.Interval == "" {
		s.DiskMonitor.Interval = "1h"
	}
	if s.NetworkUpload.Interval == "" {
		s.NetworkUpload.Interval = "1m"
	}
	if s.NetworkDownload.Interval == "" {
		s.NetworkDownload.Interval = "1m"
	}
	if s.MemoryMonitor.ThresholdPercent == 0 {
		s.MemoryMonitor.ThresholdPercent = 90
	}
	if s.MemoryMonitor.SwapThresholdPercent == 0 {
		s.MemoryMonitor.SwapThresholdPercent = 80
	}
	if s.MemoryMonitor.Interval == "" {
		s.MemoryMonitor.Interval = "5m"
	}
	if s.Memory.Root == "" {
		s.Memory.Root = "./agent_memory"
	}
	if s.AISysAdmin.Interval == "" {
		s.AISysAdmin.Interval = "5m"
	}
	if s.Loop.Interval == "" {
		s.Loop.Interval = "60s"
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
