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
  "agent": { "hostname": "...", "platform": "linux", "os": "Ubuntu 24.04", "buildTime": "...", "purpose": "<optional, from config/purpose.txt>" },
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
| `check_security_posture` | Security posture drift: listening ports (with owning process), login-capable users, admin group membership, sudoers fingerprint, per-user authorized_keys fingerprints, setuid binaries. Reports what changed since the previous snapshot; drift sets a `warning` skillstate. Snapshots stored in the `check_security_posture` domain. |
| `read_agent_memory` | Lets the LLM read the agent's own memory store. `history=true` with a `window` duration (default `"1h"`, up to 90 days) bounds how much of the tiered history is returned. |
| `compare_to_baseline` | Returns a domain's current snapshot plus the nearest retained snapshots from ~1h, ~24h, and ~7d ago so the agent can judge readings against the host's own baseline (growth rates, anomalies, time-to-full). |
| `run_diagnostic_command` | Executes a read-only shell command immediately with no approval gate |

### Action tools (require HITL or are used for reporting)

| Tool | Description |
|---|---|
| `report_findings` | Writes an observation, action, recommendation, or approval-needed entry to the `timeline` memory domain. Surfaced via `GET /status`. |
| `manage_incidents` | Reads and updates the incident ledger (`list`, `report`, `log_action`, `resolve`). See "Incident ledger" below. |
| `request_approval` | Blocking HITL gate. Writes a `pending` `ApprovalEntry` to AttrsStore and blocks until an operator decides or the 24-hour window expires. Used internally by the patch flow. See below. |
| `run_approved_command` | Files an **async** approval and returns immediately; the command executes when the operator approves (see "Async approval flow"). Commands matching a standing approval policy execute immediately instead. |

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

### Async approval flow (run_approved_command)

`run_approved_command` never blocks on the operator. It calls `request_approval.SubmitApproval` with `AutoExecute=true`, which writes the pending `ApprovalEntry` + timeline card (+ immediate notification for high risk) and returns the approval ID at once. The tool tells the model the command has NOT run and will execute on approval.

- On **approve** (`POST /approvals/:id/decision`), the approvalapi handler launches `run_approved_command.ExecuteOnApproval` in a detached goroutine: it runs the command (same sudo / manual-run escalation path), records the output on the approval entry (`Output`) and as an `action` timeline entry, notifies the operator of the result, and counts the approval for standing-policy promotion.
- On **reject**, nothing executes.
- Pending async approvals **survive agent restarts** (there is no waiting goroutine to cancel) and are expired by a background sweeper (`request_approval.StartExpirySweeper`, every minute) after 24 hours, with an "action NOT taken" notification.

Because the run finishes immediately, a pending approval no longer parks the responsibility's `Running` flag — monitoring continues at full cadence while approvals wait.

### request_approval (blocking)

Used by flows that need the decision in-run (the patch pipeline applies updates in the same 03:00 run after approval):

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

### OS-specific responsibility system prompt

The system prompt used for every responsibility run is loaded the same way: `config.Load` looks for `<goos>-system-prompt.txt` (e.g. `linux-system-prompt.txt`) in the same directory as the main config file. The canonical prompts are versioned in-repo at `config/linux-system-prompt.txt` and `config/windows-system-prompt.txt` and installed by `deploy/linux/deploy.sh` alongside the responsibilities file. Precedence: a `responsibility_system_prompt` value in `config.yaml` (host-specific override) > the OS prompt file > a built-in default compiled into the binary.

### System purpose

`config.Load` also looks for an optional `purpose.txt` next to the main config file — a short operator-authored description of what the host is for (e.g. "Primary database for internal apps"), seeded from the inventory CSV's `purpose` column and installed by `deploy/linux/deploy.sh`. When present, it is appended as an instruction block to **both** `Agent.SystemPrompt` (the interactive chat agent) and `ResponsibilitySystemPrompt` (every scheduled responsibility run) — the two prompts every tool call runs under — telling the model to weigh the stated purpose before flagging normal purpose-serving activity as a problem or recommending a core service be stopped. It is also reported back over `GET /status` (`agent.purpose`) so central-backend's fleet view and fleet intelligence analysis reflect it live, without keeping a separate copy.

## Login Monitoring

Two background monitors start alongside the loop:

- `loginmonitor` — watches auth logs for successful interactive logins. Fires a critical alert if the source address is not in `login_monitor.allowed_sources` (CIDRs or exact IPs).
- `loginmonitor.FailedMonitor` — tracks consecutive failed login attempts per source IP. Fires a critical alert when the count reaches `login_monitor.failed_login_threshold` (default 3).

`loginmonitor` also compares every new login against this host's own `login_history` — no configuration required. A login is flagged `unusual` (surfaced in `login_history`/`GET /interactive-logins`, `skillstate`/`GET /status`, and the incident ledger) when, in priority order:
1. **`new_user`** — this username has no prior login in history. Critical if remote, warning if local.
2. **`new_source`** — the username has history, but this source (IP, falling back to hostname, falling back to "local") has never been seen for that user. Critical if remote, warning if local.
3. **`unusual_time`** — the username has at least `login_monitor.baseline_min_events` (default 5) prior logins, and the current login's hour-of-day has never occurred in their history. Always warning.

Only critical-tier hits (`new_user`/`new_source` from a remote source) send email; all hits are reported to the incident ledger so recurrences are deduplicated and surfaced to the agent's next responsibility run via `incidents.OpenSummary()`. Set `login_monitor.disable_unusual_login_baseline: true` to turn this off.

## Network Connection Monitoring

`connmonitor` polls active TCP/UDP connections (default every `network_monitor.poll_interval`, 10s) and records open/close history, the same pattern `loginmonitor` uses for logins. It also compares every newly-opened connection against this host's own `connection_history` — no configuration required. A connection is flagged `unusual` (surfaced in `connection_history`/`GET /network-connections`, `skillstate`/`GET /status`, and the incident ledger) when, in priority order:
1. **`new_inbound_port`** — this (protocol, local port) has never before accepted inbound traffic on this host. Critical.
2. **`new_process`** — the owning process (when resolvable) has never before made a network connection in recorded history. Critical.
3. **`new_remote_host`** — an outbound connection to a remote address never before seen in this host's history. Warning (IP churn from CDNs/load balancers makes this a noisier signal than the identity-based checks above).

Only critical-tier hits (`new_inbound_port`/`new_process`) send email; all hits are reported to the incident ledger. Set `network_monitor.disable_unusual_connection_baseline: true` to turn this off.

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
  baseline_min_events: 5
  disable_unusual_login_baseline: false

network_monitor:
  poll_interval: "10s"
  history_limit: 2000
  disable_unusual_connection_baseline: false

responsibilities:
  - name: daily-patch-check
    time: "03:00"
    instruction: "Check for pending system patches..."
    tools: [check_for_pending_system_patches, report_findings, request_approval]
    when_to_notify: on_error

# optional host-specific override; normally the prompt comes from
# <goos>-system-prompt.txt next to this file (see above)
#responsibility_system_prompt: |
#  You are agent_patches, an AI system administrator...
```

Defaults applied by `config.Load`:
- `server.host` → `0.0.0.0`
- `server.port` → `8080`
- `security.scheme` → `none`
- `memory.root` → `./agent_memory`
- `loop.heartbeat` → `1s`
- `login_monitor.failed_login_threshold` → `3`
- `login_monitor.baseline_min_events` → `5`
- `responsibility_system_prompt` → `<goos>-system-prompt.txt` next to the config file, else a built-in prompt with tool selection rules

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
