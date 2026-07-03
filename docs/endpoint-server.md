# Endpoint Server

The endpoint-server is a Go binary that runs on each managed host. It exposes an HTTP server and drives an autonomous LLM agent loop for scheduled and on-demand system administration tasks.

## HTTP API

| Endpoint | Method | Auth required | Description |
|---|---|---|---|
| `/.well-known/agent.json` | GET | No | A2A agent card — skills list, transport, security scheme |
| `/` | POST | Yes (if bearer) | A2A JSON-RPC — `message/send` method, synchronous |
| `/status` | GET | Yes (if bearer) | Health, timeline, running tasks, disk/SMART trends |
| `/memory` | GET | Yes (if bearer) | Full memory dump (all domains + all attrs) |
| `/memory` | DELETE | Yes (if bearer) | Clear all memory |
| `/approvals/` | GET | Yes (if bearer) | List approval entries from AttrsStore |
| `/approvals/:id/decision` | POST | Yes (if bearer) | Submit an operator approve/reject decision |
| `/responsibilities` | GET | Yes (if bearer) | Responsibilities with last run state and next run time |
| `/policies` | GET | Yes (if bearer) | List standing approval policies |
| `/policies` | POST | Yes (if bearer) | Create a standing approval policy `{description, pattern, risk}` |
| `/policies/:id` | DELETE | Yes (if bearer) | Remove a standing approval policy |

### GET /status response shape

```json
{
  "agent": { "hostname": "...", "platform": "linux", "os": "Ubuntu 24.04", "buildTime": "..." },
  "status": {
    "state": "idle | active | attention",
    "lastPoll": "<RFC3339>",
    "currentTask": "<responsibility name or null>",
    "currentTasks": ["..."]
  },
  "timeline": [ /* TimelineEntry objects, newest first */ ],
  "lastPatchedAt": "<RFC3339 or null>",
  "statusDescription": "<human-readable summary when state=attention>",
  "diskTrends": { /* or null */ },
  "smartTrends": { /* or null */ }
}
```

`state` is `attention` when the timeline contains a `critical` severity entry or a pending approval with `medium` or `high` risk. Routine low-risk patch approvals do not trigger `attention`.

The status response merges in `skillstate` data: if a check/analyze skill wrote a non-OK health state to attrs (e.g. `skillstate:check_drives`), it appears as an `observation` entry in the timeline even if `report_findings` was never called.

## A2A Layer

### agent.go (`a2a/agent/agent.go`)

Drives the OpenAI chat completions tool-use loop. On each iteration:
1. Sends the current `messages` array plus available tools to the LLM
2. If `finish_reason = tool_calls`, executes each requested tool
3. Passes each result through `sanitize.ToolOutput` before appending to messages
4. Repeats until `finish_reason != tool_calls` or `MaxIter` is reached

**Duplicate call detection:** tracks call signatures (tool name + arguments). On the second call with identical arguments, returns the cached result with a prompt nudging the model to write its final answer. On the third, returns the cached result and terminates with a note to the caller.

### executor.go (`a2a/executor/executor.go`)

Implements the `a2asrv.AgentExecutor` interface. Extracts plain text from the incoming A2A message and calls `agent.Agent.Run`. Yields the result as a single agent message.

### tool.go (`a2a/tool/tool.go`)

The `Tool` interface:
```go
type Tool interface {
    Name() string
    Description() string
    InputSchema() json.RawMessage
    Execute(ctx context.Context, input json.RawMessage) (string, error)
}
```

### registry.go (`a2a/registry/registry.go`)

Holds the set of registered tools. `tool.New` is a generic constructor that derives the JSON schema from the input struct using reflection.

## Tools

All tools are registered in `endpoint-server/main.go`.

### Observation tools (read-only, no approval required)

| Tool | Description |
|---|---|
| `ping` | Connectivity test |
| `capture_system_info` | Static host metadata: OS, CPUs, RAM, disks, network interfaces |
| `check_for_pending_system_patches` | Dry-run update check. Debian: `apt-get upgrade --dry-run`. Fedora/RHEL: `dnf check-update`. Windows: PowerShell WUA COM API search. |
| `check_reboot_required` | Checks if the OS requires a reboot without applying any updates |
| `check_drives` | Disk usage + SMART health (ATA and NVMe) + trend tracking. Stores raw attribute values in attrs for trend analysis over the 60-minute history window. |
| `check_containers` | Docker, Podman, and Kubernetes container health |
| `check_nfs` | NFS mount health. On Linux, lazy-unmounts stale mounts via `umount -l`. |
| `analyze_cpu_utilization` | CPU usage over time, per-core breakdown |
| `analyze_memory_utilization` | RAM and swap usage |
| `analyze_network_utilization` | Per-interface traffic statistics |
| `check_interactive_logins` | Active interactive login sessions |
| `read_agent_memory` | Lets the LLM read the agent's own memory store. `history=true` with a `window` duration (default `"1h"`, up to 90 days) bounds how much of the tiered history is returned. |
| `compare_to_baseline` | Returns a domain's current snapshot plus the nearest retained snapshots from ~1h, ~24h, and ~7d ago so the agent can judge readings against the host's own baseline (growth rates, anomalies, time-to-full). |
| `run_diagnostic_command` | Executes a read-only shell command immediately with no approval gate |

### Action tools (require HITL or are used for reporting)

| Tool | Description |
|---|---|
| `report_findings` | Writes an observation, action, recommendation, or approval-needed entry to the `timeline` memory domain. Surfaced via `GET /status`. |
| `manage_incidents` | Reads and updates the incident ledger (`list`, `report`, `log_action`, `resolve`). See "Incident ledger" below. |
| `request_approval` | HITL gate. Writes a `pending` `ApprovalEntry` to AttrsStore and blocks until an operator decides or the 24-hour window expires. See below. |
| `run_approved_command` | Executes a state-changing shell command after operator approval — or immediately when the command matches a standing approval policy. See "Standing approval policies" below. |

`check_for_pending_system_patches` also drives the update application path (calls `Patcher.Run`), which uses `request_approval` internally before applying changes.

### run_diagnostic_command vs run_approved_command

The system prompt enforces a strict separation:
- `run_diagnostic_command` — any read-only command (`ps`, `df`, `ls`, `grep`, `journalctl`, `Get-Service`, etc.). Executes immediately.
- `run_approved_command` — any state-changing command (package install/remove, service start/stop, file delete, config change). Requires HITL approval before execution.

The LLM is instructed never to route a read-only command through `run_approved_command`.

### Incident ledger

The `incidents` package stores fingerprinted incidents (`{fingerprint, title, detail, severity, status, firstSeen, lastSeen, timesSeen, actions, resolution}`) in AttrsStore under the `incidents` key, with a per-store mutex serialising read-modify-write across concurrent responsibility runs.

- Before each responsibility run, `loop.execute` appends the open incidents (with dedupe instructions) to the responsibility's instruction, so the agent starts every run knowing what is already tracked.
- The `manage_incidents` tool lets the agent open incidents (stable kebab-case fingerprints like `disk-full-var`), record recurrences, log actions taken, and resolve incidents that have cleared.
- Resolved incidents are pruned after 30 days; the ledger is capped at 100 entries (resolved dropped first).

### Standing approval policies

The `policy` package stores operator-created policies in AttrsStore under `approval_policies`. Each policy has a Go-regex `pattern` that is matched against the ENTIRE (whitespace-normalised) command — the match is anchored, so a chained command containing an approved prefix does not match.

- Policies are managed exclusively through the `/policies` HTTP endpoints; the agent has no tool to create or modify them.
- In `run_approved_command`, after all validation checks, a command matching an enabled policy executes immediately (same sudo/manual-run path as an approved command), records an `action` timeline entry naming the policy, and prefixes its output with a policy note.
- Every operator approval is counted per normalised command (`approval_history`). At 3 approvals of the same command, the tool result and a `recommendation` timeline entry suggest promoting it to a standing policy.

### request_approval

- Writes an `ApprovalEntry` to `AttrsStore` under key `approval:<uuid>`
- Writes a `TimelineEntry` (type=approval, status=pending) to the `timeline` domain
- For `risk="high"`: fires `notifier.Notify` immediately (out-of-band alert)
- Polls `AttrsStore` every 5 seconds until the status changes from `pending`
- Single 24-hour timeout — permanently cancelled on expiry, not retried
- On timeout: fires a second `notifier.Notify` to confirm the action was NOT taken
- Returns: `"approved"`, `"rejected"`, or `"timed_out"`

## Storage (tasks.jsonl)

`utils/storage/storage.go` — `WrapAll` decorates every tool with a `storedTool` that appends a `TaskRecord` to `tasks.jsonl` after each execution.

```json
{"id":"...","name":"check_drives","input":{...},"result":"...","executed_at":"..."}
```

Storage failures are logged but never propagate — a failing write never aborts a tool execution.

## Background Loop

`loop/loop.go` — wakes on a configurable heartbeat (default 1s). On each tick, checks every registered `Responsibility` against its schedule.

For each due responsibility:
1. Sets an `atomic.Bool` to mark it as running (prevents overlapping runs)
2. Launches a goroutine that creates a new `agent.Agent` with the responsibility's filtered tool set
3. Calls `agent.Run(instruction)` — the instruction is the responsibility's `Instruction` with any open incidents from the ledger appended (see "Incident ledger"), so the agent dedupes against known problems instead of re-reporting them
4. Persists run state (`lastRunAt`, `status`, `summary`) to `AttrsStore` under `responsibility_run:<name>`
5. Fires a notification based on `when_to_notify`: `"always"`, `"on_error"` (default), or `"never"`

If a responsibility is still in flight when its next interval fires, that run is skipped and an error is logged.

## Responsibilities Scheduling

`loop/responsibility.go` — two schedule modes:

- `frequency` — a Go duration string (e.g. `"1h"`, `"30m"`). Fires repeatedly at that interval. Starts immediately on process start.
- `time` — a wall-clock time in `HH:MM` format. Fires once per day at that local time.

Exactly one of `frequency` or `time` must be set per responsibility.

### OS-specific responsibility files

On startup, `config.Load` looks for `<goos>-responsibilities.yaml` in the same directory as the main config file (e.g. `linux-responsibilities.yaml`). These define the default scheduled tasks for each OS. Per-host overrides in the main `config.yaml` take precedence: if a per-host entry shares a name with an OS-level entry, the OS-level entry is dropped.

## Login Monitoring

Two background monitors start alongside the loop:

- `loginmonitor` — watches auth logs for successful interactive logins. Fires a critical alert if the source address is not in `login_monitor.allowed_sources` (CIDRs or exact IPs).
- `loginmonitor.FailedMonitor` — tracks consecutive failed login attempts per source IP. Fires a critical alert when the count reaches `login_monitor.failed_login_threshold` (default 3).

Alerts are written via `notifier.Notify` to the `notifications` memory domain.

## Configuration

Config file at `config.yaml` or `$AGENT_PATCHES_CONFIG`. Key sections:

```yaml
agent:
  model: gpt-4o
  max_tokens: 4096
  max_iterations: 10
  api_key: <optional, defaults to OPENAI_API_KEY env var>
  base_url: <optional, for Azure or local models>

server:
  host: 0.0.0.0
  port: 9976
  public_url: <optional, embedded in agent card>

security:
  scheme: bearer   # "none" | "bearer"
  token: <secret>

memory:
  root: /opt/agent_patches/data/memory

loop:
  heartbeat: "1s"

login_monitor:
  allowed_sources: ["10.0.0.0/8", "192.168.1.5"]
  failed_login_threshold: 3

responsibilities:
  - name: daily-patch-check
    time: "03:00"
    instruction: "Check for pending system patches..."
    tools: [check_for_pending_system_patches, report_findings, request_approval]
    when_to_notify: on_error

responsibility_system_prompt: |
  You are agent_patches, an AI system administrator...
```

Defaults applied by `config.Load`:
- `server.host` → `0.0.0.0`
- `server.port` → `8080`
- `security.scheme` → `none`
- `memory.root` → `./agent_memory`
- `loop.heartbeat` → `1s`
- `login_monitor.failed_login_threshold` → `3`
- `responsibility_system_prompt` → built-in prompt with tool selection rules

## Startup Sequence

1. Detect Windows Service context; if so, run inside the Windows Service host
2. Load config
3. Set up logger
4. Set up OTel tracing (service name: `endpoint-server-<FQDN>`)
5. Create storage, memory, notifier
6. Register all tools
7. Run `capture_system_info.Gather()` once and append host metadata to `ResponsibilitySystemPrompt`
8. Auto-inject NFS and container health responsibilities if the relevant subsystems are detected
9. Wrap all tools with `storage.WrapAll`
10. Create `agent.Agent` and `executor.Executor`
11. Build A2A agent card and HTTP mux
12. Start login monitors, background loop
13. Start HTTP server

## Platform Support

Skills use Go build tags for platform-specific implementations:

- `//go:build linux` — Linux-only paths (e.g. NFS, systemd journal)
- `//go:build !windows` — Linux + macOS shared paths (e.g. smartctl)
- `//go:build windows` — Windows-only paths

The binary can be cross-compiled for all three platforms. `make run` cross-compiles for all targets.

On Linux (non-root): patching, SMART, NFS unmount, and `run_approved_command` all prepend `sudo -n` to escalate via the sudoers allowlist. See [security.md](security.md).

## OTel Tracing

Spans emitted:

| Span name | Attributes |
|---|---|
| `agent.run` | `model`, `llm.request.max_tokens`, `llm.request.max_iterations`, `llm.system_prompt`, `llm.user_input` |
| `llm.inference` | `iteration`, `model`, `llm.request.tool_count`, `llm.request.messages`, usage tokens, `finish_reason` |
| `tool.call` | `tool.name`, `tool.arguments`, `tool.result`, `security.sanitized`, `security.sanitize_events` |
| `responsibility.run` | `responsibility.name` |

Configured via standard `OTEL_*` environment variables. Set `OTEL_EXPORTER_OTLP_ENDPOINT` to enable; unset disables tracing entirely.
