package aisysadmin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/tool"
	"agent_patches/endpoint-server/utils/config"
	"agent_patches/endpoint-server/utils/notifier"
)

const (
	defaultInterval = 5 * time.Minute
	maxIterPerStep  = 20
)

// researchSystemPrompt drives Step 1: data collection and analysis.
const researchSystemPrompt = `You are agent_patches, an AI system administrator monitoring a server.

TASK — STEP 1: RESEARCH

Use the read_memory tool to gather a complete picture of the system's current state
and recent history. Read all of these domains, using history=true for each:
  • disk        — disk space usage per mount point
  • memory      — RAM and swap usage
  • net_upload  — outbound network rate (MB/s)
  • net_download — inbound network rate (MB/s)
  • logins      — user login events

After collecting the data, analyze it for:
  - Absolute levels: any resource above 80% usage
  - Trends: any resource growing rapidly over the history window
  - Unusual spikes: rates or usage that jumped significantly
  - Security: unexpected login events or logins from remote hosts

Produce a concise "State of Affairs" report structured as follows:

  RESOURCE SUMMARY
  (one line per resource: name, current value, trend)

  TRENDS
  (notable changes observed in the history; "None" if flat)

  ANOMALIES
  (specific findings that warrant attention; "None" if clean)

  RECENT LOGINS
  (summary of login events; "None recorded" if no data)

  HEALTH ASSESSMENT: <OK|WARNING|CRITICAL>
  (one-line justification)

Be thorough — use the tools for every domain before writing the report.`

// planningSystemPrompt drives Step 2: pure reasoning, no tools.
const planningSystemPrompt = `You are agent_patches, an AI system administrator.

TASK — STEP 2: PLANNING

Read the State of Affairs report below and decide what (if anything) needs to be done.

For each action you identify:
  1. State the problem it addresses.
  2. Describe the action precisely.
  3. Classify it: DIAGNOSTIC (read-only) or CORRECTIVE (modifies system state).
  4. List the exact shell command(s) to run (or "N/A" for investigation steps).

Guidelines:
  - If HEALTH ASSESSMENT is OK and ANOMALIES is None, output "NO ACTIONS REQUIRED" and stop.
  - Be conservative. Do not invent problems or take speculative action.
  - Prefer diagnostics before corrections.
  - Never schedule reboots or service restarts unless a clear problem demands it.`

// actionSystemPrompt drives Step 3: tool execution.
const actionSystemPrompt = `You are agent_patches, an AI system administrator.

TASK — STEP 3: ACTION

Execute the action plan below using the available tools.

For each planned action:
  1. Log what you are about to do (include the action number and description).
  2. Run the command with run_command, setting reason to the action description.
  3. Report the output and note whether the action succeeded.

After completing all actions (or confirming nothing is needed), write:

  ACTION SUMMARY
  (list each action taken with outcome; or "No actions taken" if plan said NO ACTIONS REQUIRED)`

// SysAdmin is a background AI agent that periodically analyses system state
// from the memory store and takes corrective action when needed.
type SysAdmin struct {
	cfg      *config.AISysAdminSettings
	agentCfg *config.AgentSettings
	mem      *memory.Store
	notifier *notifier.Notifier
}

// New creates a SysAdmin. Call Start to launch the background loop.
func New(cfg *config.AISysAdminSettings, agentCfg *config.AgentSettings, mem *memory.Store, n *notifier.Notifier) *SysAdmin {
	return &SysAdmin{cfg: cfg, agentCfg: agentCfg, mem: mem, notifier: n}
}

// Start launches the background analysis loop. It returns immediately; the
// goroutine exits when ctx is cancelled.
func (s *SysAdmin) Start(ctx context.Context) {
	if !s.cfg.Enabled {
		slog.Info("ai_sysadmin: disabled")
		return
	}
	interval, err := time.ParseDuration(s.cfg.Interval)
	if err != nil || interval <= 0 {
		slog.Error("ai_sysadmin: invalid interval, defaulting to 5m",
			"interval", s.cfg.Interval, "error", err)
		interval = defaultInterval
	}
	slog.Info("ai_sysadmin: starting", "interval", interval, "model", s.model())
	go s.loop(ctx, interval)
}

func (s *SysAdmin) loop(ctx context.Context, interval time.Duration) {
	s.run(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("ai_sysadmin: stopped")
			return
		case <-ticker.C:
			s.run(ctx)
		}
	}
}

func (s *SysAdmin) model() string {
	if s.cfg.Model != "" {
		return s.cfg.Model
	}
	return s.agentCfg.Model
}

func (s *SysAdmin) newClient() openai.Client {
	opts := []option.RequestOption{}
	if s.agentCfg.APIKey != "" {
		opts = append(opts, option.WithAPIKey(s.agentCfg.APIKey))
	}
	if s.agentCfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(s.agentCfg.BaseURL))
	}
	return openai.NewClient(opts...)
}

// run executes one full research → plan → act cycle.
func (s *SysAdmin) run(ctx context.Context) {
	runID := time.Now().Format("20060102-150405")
	log := slog.With("run_id", runID, "component", "ai_sysadmin")

	log.Info("ai_sysadmin: ════ cycle start ════")
	cycleStart := time.Now()

	client := s.newClient()
	var commandsRun []string

	readTools := newReadTools(s.mem, log)
	actTools := append(readTools, newRunCommandTool(&commandsRun, log))

	// ── Step 1: Research ─────────────────────────────────────────────────
	log.Info("ai_sysadmin: step 1/3 — research: reading system state")
	report, err := runStep(ctx, client, s.model(), s.agentCfg.MaxTokens, maxIterPerStep, log,
		researchSystemPrompt,
		"Research all memory domains and produce the State of Affairs report now.",
		readTools,
	)
	if err != nil {
		log.Error("ai_sysadmin: research step failed", "error", err)
		return
	}
	log.Info("ai_sysadmin: step 1/3 complete — research",
		"report_chars", len(report),
		"health", extractHealth(report),
	)
	log.Debug("ai_sysadmin: full research report", "report", report)

	// ── Step 2: Plan ─────────────────────────────────────────────────────
	log.Info("ai_sysadmin: step 2/3 — planning: analysing report")
	plan, err := runStep(ctx, client, s.model(), s.agentCfg.MaxTokens, maxIterPerStep, log,
		planningSystemPrompt,
		fmt.Sprintf("STATE OF AFFAIRS REPORT:\n\n%s", report),
		nil, // pure reasoning — no tools
	)
	if err != nil {
		log.Error("ai_sysadmin: planning step failed", "error", err)
		return
	}
	noAction := strings.Contains(strings.ToUpper(plan), "NO ACTIONS REQUIRED")
	log.Info("ai_sysadmin: step 2/3 complete — planning",
		"plan_chars", len(plan),
		"no_action", noAction,
	)
	log.Debug("ai_sysadmin: full action plan", "plan", plan)

	// ── Step 3: Act ──────────────────────────────────────────────────────
	log.Info("ai_sysadmin: step 3/3 — acting: executing plan")
	actSummary, err := runStep(ctx, client, s.model(), s.agentCfg.MaxTokens, maxIterPerStep, log,
		actionSystemPrompt,
		fmt.Sprintf("ACTION PLAN:\n\n%s", plan),
		actTools,
	)
	if err != nil {
		log.Error("ai_sysadmin: action step failed", "error", err)
		// fall through — still notify if needed
	}
	log.Info("ai_sysadmin: step 3/3 complete — acting",
		"commands_run", len(commandsRun),
		"summary_chars", len(actSummary),
	)
	log.Debug("ai_sysadmin: full action summary", "summary", actSummary)

	elapsed := time.Since(cycleStart)
	log.Info("ai_sysadmin: ════ cycle complete ════",
		"elapsed", elapsed.Round(time.Millisecond),
		"commands_run", len(commandsRun),
	)

	// ── Notification ─────────────────────────────────────────────────────
	anomaly := hasAnomaly(report)
	if anomaly || len(commandsRun) > 0 {
		log.Info("ai_sysadmin: sending notification",
			"anomaly", anomaly,
			"commands_run", len(commandsRun),
		)
		host, _ := os.Hostname()
		subj, body := buildNotification(host, runID, report, plan, actSummary, commandsRun, anomaly)
		s.notifier.Notify(ctx, subj, body)
	} else {
		log.Info("ai_sysadmin: system healthy — no notification sent")
	}
}

// runStep drives a single-purpose LLM completion loop with optional tool use.
// It returns the final text content from the model.
func runStep(
	ctx context.Context,
	client openai.Client,
	model string,
	maxTokens int,
	maxIter int,
	log *slog.Logger,
	systemPrompt, userMsg string,
	tools []tool.Tool,
) (string, error) {
	toolIndex := make(map[string]tool.Tool, len(tools))
	oaiTools := make([]openai.ChatCompletionToolParam, 0, len(tools))
	for _, t := range tools {
		toolIndex[t.Name()] = t
		var schema openai.FunctionParameters
		_ = json.Unmarshal(t.InputSchema(), &schema)
		oaiTools = append(oaiTools, openai.ChatCompletionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        t.Name(),
				Description: param.NewOpt(t.Description()),
				Parameters:  schema,
			},
		})
	}

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage(systemPrompt),
		openai.UserMessage(userMsg),
	}

	for iter := 0; iter < maxIter; iter++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		req := openai.ChatCompletionNewParams{
			Model:     model,
			MaxTokens: param.NewOpt(int64(maxTokens)),
			Messages:  messages,
		}
		if len(oaiTools) > 0 {
			req.Tools = oaiTools
		}

		log.Debug("ai_sysadmin: LLM request",
			"iter", iter+1,
			"model", model,
			"messages", len(messages),
			"tools_available", len(oaiTools),
		)

		resp, err := client.Chat.Completions.New(ctx, req)
		if err != nil {
			return "", fmt.Errorf("LLM API call: %w", err)
		}
		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("LLM returned empty choices")
		}

		choice := resp.Choices[0]
		log.Debug("ai_sysadmin: LLM response",
			"iter", iter+1,
			"finish_reason", choice.FinishReason,
			"tool_calls", len(choice.Message.ToolCalls),
			"content_chars", len(choice.Message.Content),
			"prompt_tokens", resp.Usage.PromptTokens,
			"completion_tokens", resp.Usage.CompletionTokens,
		)

		if choice.FinishReason != "tool_calls" {
			return choice.Message.Content, nil
		}

		// Append the assistant turn (with pending tool calls) to the conversation.
		messages = append(messages, choice.Message.ToParam())

		// Execute each requested tool call.
		for _, tc := range choice.Message.ToolCalls {
			t, ok := toolIndex[tc.Function.Name]
			if !ok {
				log.Warn("ai_sysadmin: model requested unknown tool",
					"tool", tc.Function.Name)
				messages = append(messages,
					openai.ToolMessage("unknown tool: "+tc.Function.Name, tc.ID))
				continue
			}
			log.Info("ai_sysadmin: tool call dispatched",
				"tool", tc.Function.Name,
				"args_preview", truncate(tc.Function.Arguments, 120),
			)
			result, execErr := t.Execute(ctx, json.RawMessage(tc.Function.Arguments))
			if execErr != nil {
				log.Warn("ai_sysadmin: tool execution error",
					"tool", tc.Function.Name, "error", execErr)
				messages = append(messages,
					openai.ToolMessage(fmt.Sprintf("error: %v", execErr), tc.ID))
				continue
			}
			log.Debug("ai_sysadmin: tool result",
				"tool", tc.Function.Name,
				"result_chars", len(result),
				"result_preview", truncate(result, 120),
			)
			messages = append(messages, openai.ToolMessage(result, tc.ID))
		}
	}

	return "", fmt.Errorf("max iterations (%d) exceeded without a final response", maxIter)
}

// hasAnomaly returns true when the report signals a non-OK health status.
func hasAnomaly(report string) bool {
	upper := strings.ToUpper(report)
	return strings.Contains(upper, "HEALTH ASSESSMENT: WARNING") ||
		strings.Contains(upper, "HEALTH ASSESSMENT: CRITICAL") ||
		strings.Contains(upper, "ANOMAL")
}

// extractHealth pulls the HEALTH ASSESSMENT line from the report for logging.
func extractHealth(report string) string {
	for _, line := range strings.Split(report, "\n") {
		if strings.Contains(strings.ToUpper(line), "HEALTH ASSESSMENT") {
			return strings.TrimSpace(line)
		}
	}
	return "unknown"
}

// buildNotification composes the email subject and body for a sysadmin alert.
func buildNotification(host, runID, report, plan, actSummary string, cmdsRun []string, anomaly bool) (subject, body string) {
	var label string
	switch {
	case anomaly && len(cmdsRun) > 0:
		label = "Anomaly Detected + Actions Taken"
	case anomaly:
		label = "Anomaly Detected"
	default:
		label = "Actions Taken"
	}

	subject = fmt.Sprintf("[%s] AI Sysadmin: %s (%s)", host, label, runID)

	var sb strings.Builder
	fmt.Fprintf(&sb, "AI Sysadmin Cycle Report\n")
	fmt.Fprintf(&sb, "Host:      %s\n", host)
	fmt.Fprintf(&sb, "Run ID:    %s\n", runID)
	fmt.Fprintf(&sb, "Generated: %s\n", time.Now().UTC().Format(time.RFC1123))
	fmt.Fprintf(&sb, "\n")

	section(&sb, "STATE OF AFFAIRS", report)
	section(&sb, "ACTION PLAN", plan)

	if len(cmdsRun) > 0 {
		fmt.Fprintf(&sb, "══════════════════════════════════════\n")
		fmt.Fprintf(&sb, "COMMANDS EXECUTED (%d)\n", len(cmdsRun))
		fmt.Fprintf(&sb, "══════════════════════════════════════\n")
		for i, cmd := range cmdsRun {
			fmt.Fprintf(&sb, "  %d. %s\n", i+1, cmd)
		}
		fmt.Fprintf(&sb, "\n")
	}

	if actSummary != "" {
		section(&sb, "ACTION SUMMARY", actSummary)
	}

	return subject, sb.String()
}

func section(sb *strings.Builder, title, content string) {
	fmt.Fprintf(sb, "══════════════════════════════════════\n")
	fmt.Fprintf(sb, "%s\n", title)
	fmt.Fprintf(sb, "══════════════════════════════════════\n")
	fmt.Fprintf(sb, "%s\n\n", strings.TrimSpace(content))
}
