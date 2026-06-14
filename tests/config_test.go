package tests

import (
	"os"
	"strings"
	"testing"

	"agent_patches/endpoint-server/utils/config"
)

const validYAML = `
agent:
  model: claude-opus-4-7
  max_tokens: 1024
  max_iterations: 5
  system_prompt: test prompt

logging:
  level: debug

tasks:
  enabled:
    - hello
    - disk

storage:
  tasks_file: /tmp/test_tasks.jsonl

responsibilities:
  - name: disk-space-check
    frequency: "1h"
    instruction: Check disk usage and report anything above 90%.
    tools:
      - disk
    when_to_notify: "on error"
  - name: daily-summary
    time: "07:00"
    instruction: Summarize overnight activity.
    when_to_notify: "always"
`

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp("", "agent_patches_config_*.yaml")
	if err != nil {
		t.Fatalf("could not create temp config: %v", err)
	}
	t.Cleanup(func() { os.Remove(f.Name()) })
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("could not write temp config: %v", err)
	}
	f.Close()
	return f.Name()
}

func TestLoad_ValidFile(t *testing.T) {
	path := writeTempConfig(t, validYAML)
	t.Setenv(config.EnvKey, path)

	s, err := config.Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if s.Agent.Model != "claude-opus-4-7" {
		t.Errorf("Agent.Model = %q, want %q", s.Agent.Model, "claude-opus-4-7")
	}
	if s.Agent.MaxTokens != 1024 {
		t.Errorf("Agent.MaxTokens = %d, want 1024", s.Agent.MaxTokens)
	}
	if s.Agent.MaxIter != 5 {
		t.Errorf("Agent.MaxIter = %d, want 5", s.Agent.MaxIter)
	}
	if s.Agent.SystemPrompt != "test prompt" {
		t.Errorf("Agent.SystemPrompt = %q, want %q", s.Agent.SystemPrompt, "test prompt")
	}
	if s.Logging.Level != "debug" {
		t.Errorf("Logging.Level = %q, want %q", s.Logging.Level, "debug")
	}
	if len(s.Tasks.Enabled) != 2 {
		t.Errorf("Tasks.Enabled len = %d, want 2", len(s.Tasks.Enabled))
	}
	if s.Storage.TasksFile != "/tmp/test_tasks.jsonl" {
		t.Errorf("Storage.TasksFile = %q, want %q", s.Storage.TasksFile, "/tmp/test_tasks.jsonl")
	}

	if len(s.Responsibilities) != 2 {
		t.Fatalf("Responsibilities len = %d, want 2", len(s.Responsibilities))
	}
	r0 := s.Responsibilities[0]
	if r0.Name != "disk-space-check" || r0.Frequency != "1h" || r0.WhenToNotify != "on error" {
		t.Errorf("Responsibilities[0] = %+v, unexpected values", r0)
	}
	if len(r0.Tools) != 1 || r0.Tools[0] != "disk" {
		t.Errorf("Responsibilities[0].Tools = %v, want [disk]", r0.Tools)
	}
	r1 := s.Responsibilities[1]
	if r1.Name != "daily-summary" || r1.Time != "07:00" || r1.WhenToNotify != "always" {
		t.Errorf("Responsibilities[1] = %+v, unexpected values", r1)
	}

	if s.ResponsibilitySystemPrompt == "" {
		t.Error("ResponsibilitySystemPrompt should default to a non-empty value")
	}
}

func TestLoad_ResponsibilitySystemPromptOverride(t *testing.T) {
	const yamlContent = validYAML + `
responsibility_system_prompt: "custom sysadmin prompt"
`
	path := writeTempConfig(t, yamlContent)
	t.Setenv(config.EnvKey, path)

	s, err := config.Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if s.ResponsibilitySystemPrompt != "custom sysadmin prompt" {
		t.Errorf("ResponsibilitySystemPrompt = %q, want %q", s.ResponsibilitySystemPrompt, "custom sysadmin prompt")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	t.Setenv(config.EnvKey, "/nonexistent/agent_patches/config.yaml")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() expected error for missing file, got nil")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	path := writeTempConfig(t, "agent: [unclosed")
	t.Setenv(config.EnvKey, path)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() expected error for invalid YAML, got nil")
	}
}

func TestLoad_ErrorMentionsPath(t *testing.T) {
	const sentinel = "/some/specific/config.yaml"
	t.Setenv(config.EnvKey, sentinel)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() expected error, got nil")
	}
	if !strings.Contains(err.Error(), sentinel) {
		t.Errorf("error %q should mention path %q", err.Error(), sentinel)
	}
}

func TestFilePath_ReadsEnvVar(t *testing.T) {
	const want = "/custom/path/config.yaml"
	t.Setenv(config.EnvKey, want)

	if got := config.FilePath(); got != want {
		t.Errorf("FilePath() = %q, want %q", got, want)
	}
}

func TestFilePath_DefaultsToCurrentDir(t *testing.T) {
	t.Setenv(config.EnvKey, "")

	got := config.FilePath()
	if !strings.HasSuffix(got, "config.yaml") {
		t.Errorf("FilePath() = %q, expected to end with config.yaml", got)
	}
}

func TestLoad_DefaultsToCurrentDir(t *testing.T) {
	t.Setenv(config.EnvKey, "")

	// Place a config.yaml in the working directory (tests/ when `go test` runs).
	const localCfg = "config.yaml"
	if err := os.WriteFile(localCfg, []byte(validYAML), 0o644); err != nil {
		t.Fatalf("could not write local config: %v", err)
	}
	t.Cleanup(func() { os.Remove(localCfg) })

	s, err := config.Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if s.Agent.Model != "claude-opus-4-7" {
		t.Errorf("Agent.Model = %q, want %q", s.Agent.Model, "claude-opus-4-7")
	}
}
