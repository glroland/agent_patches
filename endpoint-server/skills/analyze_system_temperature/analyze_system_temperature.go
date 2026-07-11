// Package analyze_system_temperature reports temperature sensor readings for
// the host (CPU package/core, ACPI thermal zones, or SMC sensors depending on
// OS), flags sensors above normal operating thresholds, and tracks readings
// over time so sustained overheating is caught even if a single sample looks
// fine.
package analyze_system_temperature

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skillstate"
	"agent_patches/endpoint-server/utils/config"
)

// Temperature thresholds (degrees Celsius) for the skill's last-known-state
// health. 80°C/90°C mirror the warning/critical split used elsewhere in this
// package family (CPU, disk) and match typical CPU package throttle margins.
const (
	tempWarningC  = 80.0
	tempCriticalC = 90.0
)

// TempSensor holds a point-in-time reading from one temperature sensor.
type TempSensor struct {
	Name     string // sensor label (e.g. "x86_pkg_temp", "CPU die temperature")
	CelsiusC float64
}

type tempInput struct{}

// CheckResult is the outcome of one temperature survey.
type CheckResult struct {
	Health  skillstate.Health
	Report  string
	Summary string
}

// runCheck surveys all available temperature sensors, records the worst
// health as a skillstate entry, and returns a human-readable report. It makes
// no LLM calls — callers decide whether the result warrants one.
func runCheck(ctx context.Context, mem *memory.Store) (CheckResult, error) {
	slog.Info("analyze_system_temperature: starting")
	sensors, err := localTemps(ctx)
	if err != nil {
		slog.Info("analyze_system_temperature: failed", "error", err)
		_ = skillstate.Save(mem, "analyze_system_temperature", skillstate.HealthCritical,
			fmt.Sprintf("failed to read temperature sensors: %v", err))
		return CheckResult{}, fmt.Errorf("analyze_system_temperature: %w", err)
	}

	if len(sensors) == 0 {
		slog.Info("analyze_system_temperature: completed", "sensors", 0)
		const summary = "no temperature sensors detected on this host"
		_ = skillstate.Save(mem, "analyze_system_temperature", skillstate.HealthOK, summary)
		return CheckResult{Health: skillstate.HealthOK, Report: summary + ".", Summary: summary}, nil
	}

	slog.Debug("analyze_system_temperature: read sensors", "count", len(sensors))
	health, summary := tempHealth(sensors)

	trends, terr := RecordSamples(mem, sensors, time.Now())
	if terr != nil {
		slog.Warn("analyze_system_temperature: trend recording failed", "error", terr)
	}
	if sHealth, sSummary := SustainedHealth(trends); severityOf(sHealth) > severityOf(health) {
		health, summary = sHealth, sSummary
	}

	report := BuildReport(sensors)
	_ = skillstate.Save(mem, "analyze_system_temperature", health, summary)
	slog.Info("analyze_system_temperature: completed", "sensors", len(sensors), "health", health, "output_len", len(report))
	return CheckResult{Health: health, Report: report, Summary: summary}, nil
}

// NewTemperatureTool returns a task tool that reports current temperature
// sensor readings for the host. The result is also recorded as the skill's
// last known state (see skillstate), so sustained overheating is reflected in
// GET /status even if the agent never calls report_findings.
func NewTemperatureTool(mem *memory.Store) (tool.Tool, error) {
	return tool.New(
		"analyze_system_temperature",
		"Reports current temperature sensor readings for the host (CPU package/core "+
			"thermal zones on Linux, ACPI thermal zones on Windows, SMC sensors on "+
			"macOS), flags sensors above normal operating thresholds, and tracks "+
			"readings over time to detect sustained overheating. Sensor availability "+
			"depends on hardware/firmware exposure and platform privileges.",
		func(ctx context.Context, _ tempInput) (string, error) {
			res, err := runCheck(ctx, mem)
			return res.Report, err
		},
	)
}

// NewPreCheck returns a loop.PreCheck-compatible function that runs the
// temperature survey directly, bypassing the LLM tool-use loop entirely. It
// reports needsLLM=false whenever every sensor is within normal range — the
// common case on most scheduled ticks — so the loop can skip the LLM call
// outright instead of invoking it just to have it discover nothing is wrong.
func NewPreCheck(mem *memory.Store) func(ctx context.Context) (bool, string, error) {
	return func(ctx context.Context) (bool, string, error) {
		res, err := runCheck(ctx, mem)
		if err != nil {
			// Fail open: let the LLM path see and report the failure rather
			// than silently going quiet on a broken health check.
			return true, "", err
		}
		return res.Health != skillstate.HealthOK, res.Report, nil
	}
}

// AutoResponsibility returns the built-in temperature-health-check
// responsibility and true if at least one temperature sensor is readable on
// this host. Returns false when no sensors are exposed (common on VMs, or on
// macOS/Windows without the privileges/hardware support needed), so hosts
// with no usable sensors don't get a responsibility that can never fire.
func AutoResponsibility() (config.ResponsibilitySettings, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sensors, err := localTemps(ctx)
	if err != nil || len(sensors) == 0 {
		return config.ResponsibilitySettings{}, false
	}

	return config.ResponsibilitySettings{
		Name:      "temperature-health-check",
		Frequency: "15m",
		Instruction: `A temperature sensor reading above normal operating range was
detected on this host (see the pre-gathered report included with this
instruction — no need to call analyze_system_temperature again unless you
want a fresh read). Sensors at/above 80°C are flagged as a warning; at/above
90°C, or sustained above 80°C for 15+ minutes, are flagged as critical.
Use run_diagnostic_command to gather more context if useful (e.g. checking
for a failed fan, blocked airflow, or a runaway process driving load).
Call report_findings with your assessment and remediation recommendation.`,
		Tools:        []string{"analyze_system_temperature", "run_diagnostic_command", "report_findings"},
		WhenToNotify: "on error",
	}, true
}

// tempHealth derives a skillstate health/summary pair from a set of sensor
// readings: critical if any sensor is at/above tempCriticalC, warning if any
// sensor is at/above tempWarningC, else ok.
func tempHealth(sensors []TempSensor) (skillstate.Health, string) {
	var warnings, criticals []string
	for _, s := range sensors {
		switch {
		case s.CelsiusC >= tempCriticalC:
			criticals = append(criticals, fmt.Sprintf("%s %.1f°C", s.Name, s.CelsiusC))
		case s.CelsiusC >= tempWarningC:
			warnings = append(warnings, fmt.Sprintf("%s %.1f°C", s.Name, s.CelsiusC))
		}
	}

	switch {
	case len(criticals) > 0:
		return skillstate.HealthCritical, strings.Join(criticals, "; ")
	case len(warnings) > 0:
		return skillstate.HealthWarning, strings.Join(warnings, "; ")
	default:
		return skillstate.HealthOK, "all sensors within normal range"
	}
}

// BuildReport composes a human-readable summary of temperature sensor readings.
func BuildReport(sensors []TempSensor) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Temperature\n")
	for _, s := range sensors {
		marker := ""
		switch {
		case s.CelsiusC >= tempCriticalC:
			marker = " (CRITICAL)"
		case s.CelsiusC >= tempWarningC:
			marker = " (WARNING)"
		}
		fmt.Fprintf(&sb, "  %-28s %.1f°C%s\n", s.Name+":", s.CelsiusC, marker)
	}
	return sb.String()
}

// severityOf maps a skillstate Health to a numeric level for comparison.
func severityOf(h skillstate.Health) int {
	switch h {
	case skillstate.HealthWarning:
		return 1
	case skillstate.HealthCritical:
		return 2
	default:
		return 0
	}
}
