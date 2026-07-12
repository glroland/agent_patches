# Memory System

The memory system provides durable, file-backed storage for the endpoint-server agent. It has two distinct storage types with different semantics: a history-retaining domain store and a flat key-value attribute store.

## Store (`memory/memory.go`)

`memory.Store` is the root object, created once at startup from `config.Memory.Root`.

```go
mem := memory.New(&cfg.Memory)
```

`Store.Domain(name)` returns a `DomainStore` for the named domain. `Store.Attrs()` returns the single global `AttrsStore`. Both methods return the same instance on repeated calls with the same argument, which is what makes the per-instance mutex actually serialise concurrent access.

Root directory default: `./agent_memory`. Production Linux path: `/opt/agent_patches/data/memory`.

## DomainStore

Persists timestamped JSON snapshots for one named domain. Backed by a subdirectory `<root>/<domain>/`.

### Write semantics

Each `Write(v)` call:
1. Marshals `v` to JSON
2. Creates a file named `<unix_nanoseconds>.json` (temp file + rename for atomicity)
3. Prunes the directory according to the retention policy

Retention is tiered (enforced on every write). A snapshot is assigned to the first tier whose horizon covers its age; within each tier bucket only the newest snapshot is kept:

| Tier | Horizon | Bucket (resolution) |
|---|---|---|
| Recent | 60 minutes | 5 minutes |
| Hourly | 7 days | 1 hour |
| Daily baseline | 90 days | 24 hours |

Anything older than 90 days is deleted. In steady state a domain holds roughly 12 recent + ~167 hourly + ~83 daily ≈ 260 small JSON files. The long tiers exist so the agent can compare current readings against the host's own history (growth rates, anomalies, time-to-full predictions) via the `compare_to_baseline` skill.

### Read semantics

- `ReadCurrent(v)` — deserialises the newest file into `v`. Returns an error if no files exist.
- `ReadHistory()` — returns all retained snapshots as `[]Snapshot` sorted oldest-first. Each `Snapshot` has a `Timestamp` and a `json.RawMessage`.
- `ReadNearest(target)` — returns the single retained snapshot whose timestamp is closest to `target` (either direction). Used by `compare_to_baseline` to fetch the ~1h/~24h/~7d-ago points.

### Key-mode access

Some domains are shared by multiple independent writers, each owning a distinct key (e.g. the `Network` domain holds both connection history and rate-baseline samples). For these, `DomainStore` offers `AttrsStore`-like map semantics scoped to one domain instead of the single global attrs file:

- `SetKey(key, value)` — atomically merges `key=value` into the domain's current snapshot (treated as `map[string]json.RawMessage`), preserving any other keys already present, and persists the result as a new snapshot.
- `GetKey(key, v)` — reads a single key out of the domain's current snapshot. Returns an error if the domain has no snapshot yet or the key is absent.
- `AllKeys()` — returns every key/value pair in the domain's current snapshot. Returns `nil, nil` if the domain has no snapshot yet.
- `Update(dst, mutate)` — the underlying atomic primitive: loads the current snapshot into `dst`, invokes `mutate` to modify it, and persists the result as a new snapshot, all under one lock. `SetKey`/`GetKey`/`AllKeys` are built on top of this so concurrent calls to different keys in the same domain can't lose each other's writes.

Because each `SetKey` call writes a full new timestamped snapshot (not an in-place file edit like `AttrsStore.Set`), a domain used in key mode also gets the same tiered history retention as whole-blob domains — a side benefit for keys like `disk_trends` that are trend data anyway.

### Concurrency

One mutex per `DomainStore`. `Store.Domain` returns the same instance for a given name so all callers on the same store share one mutex.

## AttrsStore

A flat key-value store backed by a single `attrs.json` file at `<root>/attrs.json`.

### File format

```json
{
  "some-key": { ... },
  "another-key": "string value",
  "count-key": 42
}
```

### Write semantics

`Set(key, value)` reads the entire file, updates the key, marshals back with `json.MarshalIndent`, and atomically replaces the file (temp + rename). There is no per-key granularity at the file level.

`Delete(key)` reads, removes the key, and writes back. No-op if the key does not exist.

### Read semantics

`Get(key, v)` reads the file, parses JSON, extracts the value for `key`, and deserialises into `v`.

`All()` returns the raw `map[string]json.RawMessage`.

### Concurrency

One mutex for the entire `AttrsStore`. All reads and writes hold it.

## Domains in Production

| Domain | Written by | Read by | Content |
|---|---|---|---|
| `timeline` | `report_findings` skill, `request_approval` skill | `GET /status`, central-backend poll | `[]TimelineEntry` — observations, actions, recommendations, approval requests, escalations. Newest first. Up to 50 entries (trimmed by request_approval). |
| `notifications` | `notifier.Notify` | central-backend (via `GET /memory`) | Latest notification: `{ subject, body, time }`. Only the most recent 60-minute window is retained. |
| `check_drives` | `check_drives` skill | `check_drives` (ReadHistory for trends) | Disk usage snapshot per run |
| `check_containers` | `check_containers` skill | — | Container health snapshot |
| `check_nfs` | `check_nfs` skill | — | NFS mount health snapshot |
| `analyze_cpu_utilization` | CPU skill | CPU skill (ReadHistory) | CPU usage snapshot |
| `analyze_memory_utilization` | memory skill | memory skill (ReadHistory) | RAM/swap snapshot |
| `analyze_network_utilization` | network skill | network skill (ReadHistory) | Network stats snapshot |
| `analyze_system_temperature` | temperature skill | temperature skill (ReadHistory for sustained-high detection) | Temperature sensor snapshot |
| `check_interactive_logins` | login skill | — | Login session snapshot |
| `check_security_posture` | `check_security_posture` skill | same skill (previous-snapshot diff), `compare_to_baseline` | Security posture snapshot: listening ports, users, admins, sudoers/authorized_keys fingerprints, setuid binaries |
| `Network` (key mode) | `connmonitor` (`connection_history` key), `analyze_network_utilization`/baseline.go (`network_rate_baseline` key) | `check_network_connections`, `GET /network-connections`, `analyze_network_utilization` (baseline anomaly detection) | `connection_history`: rolling open/close history of TCP/UDP connections with unusual-connection flags (`new_inbound_port`, `new_process`, `new_remote_host`). `network_rate_baseline`: rolling throughput (`RateSample`) history used to judge whether current traffic is anomalous. |
| `Disk Trends` (key mode) | `check_drives`/trend.go (`disk_trends` key), `check_drives`/smart_trend.go (`smart_trends` key) | `GET /status`, `check_drives` (`TrendHealth`/`SmartTrendHealth`) | `disk_trends`: per-mount usage sample history, slope, and forecast-to-full. `smart_trends`: per-device SMART attribute sample history, delta-from-baseline, and slope. |
| `Incidents` (whole blob) | `manage_incidents` skill (via `incidents.Store`) | responsibility loop (prompt injection), `manage_incidents` | `[]Incident` — the incident ledger: fingerprinted ongoing problems with first/last-seen times, occurrence counts, actions taken, and resolutions. Open incidents are appended to every responsibility instruction. Resolved incidents pruned after 30 days; ledger capped at 100 entries. |
| `Skill States` (key mode) | `check_*`/`analyze_*` skills (via `skillstate.Save`, `skill_state:<skill>` key), `loop.execute` (`persistRunState`, `responsibility_run:<name>` key) | `GET /status`, `GET /responsibilities` | `skill_state:<skill>`: last known health per skill, `{ health: ok\|warning\|critical, summary, time }` — merged into the `/status` timeline even if `report_findings` was not called. `responsibility_run:<name>`: last run outcome per responsibility, see [RunState schema](#runstate-schema). |

Skills that track trends call `ReadHistory()` on their own domain to compare current values against recent history. This is how disk growth rate and SMART attribute drift are detected. The `compare_to_baseline` skill additionally uses `ReadNearest()` to compare any domain's current snapshot against its ~1-hour, ~24-hour, and ~7-day-old baselines, and `read_agent_memory` accepts a `window` parameter (default `"1h"`) to bound how much history is returned to the model. Domains marked "key mode" above are read/written via `SetKey`/`GetKey`/`AllKeys` (see [Key-mode access](#key-mode-access)) rather than `Write`/`ReadCurrent`.

## Attrs Keys in Production

| Key pattern | Type | Written by | Read by | Description |
|---|---|---|---|---|
| `approval:<uuid>` | `ApprovalEntry` | `request_approval` | `approvalapi`, `request_approval` (poll or expiry sweeper) | HITL approval state. Status transitions: `pending` → `approved \| rejected \| timed_out \| cancelled`. Async entries carry `auto_execute: true` plus `exec_reason`, and record the command `output` after execution. |
| `last_patched_at` | RFC3339 string | `check_for_pending_system_patches` | `GET /status` | Timestamp of the last successful OS update |
| `approval_policies` | `[]Policy` | `POST /policies` (operator only) | `run_approved_command` (via `policy.Store`) | Standing approval policies: `{ id, description, pattern, risk, createdAt, enabled }`. `pattern` is a Go regex matched against the ENTIRE command (anchored). A matching command executes without a fresh HITL approval. |
| `approval_history` | `map[string]int` | `run_approved_command` after each operator approval | `run_approved_command` | Count of operator approvals per (whitespace-normalised) command. At 3 approvals of the same command, a recommendation to create a standing policy is added to the timeline. Capped at 200 entries. |
| `manual_run:<uuid>` | `ManualRunEntry` | `request_manual_run` | `manualrunapi`, `request_manual_run` (poll) | Manual-execution request state when sudoers blocks a command: `pending` → `completed \| skipped \| timed_out \| cancelled`, with the operator-pasted `output` and a copy-pasteable sudoers instruction. |
| `login_history` | login event list | `loginmonitor` | `GET /interactive-logins`, baseline comparison | Rolling history of interactive logins with unusual-login flags (`new_user`, `new_source`, `unusual_time`). |

Network connection history, disk/SMART trends, incidents, and skill/responsibility-run state used to live here as flat attrs keys; they now live in their own domains (see [Domains in Production](#domains-in-production)) so each surfaces as its own section in the `/memory` dump instead of the catch-all attrs bucket.

### ApprovalEntry schema

```json
{
  "id": "<uuid>",
  "title": "...",
  "detail": "...",
  "proposed_action": "apt-get upgrade -y",
  "importance": "low | medium | high",
  "risk": "low | medium | high",
  "status": "pending | approved | rejected | timed_out | cancelled",
  "requested_at": "<RFC3339>",
  "decided_at": "<RFC3339 or null>",
  "reason": "<operator comment or empty>",
  "auto_execute": true,
  "exec_reason": "<agent's reason, used in manual-run escalation>",
  "output": "<command output recorded after async execution>"
}
```

`importance` (urgency to act) and `risk` (blast radius of the action itself) are assessed independently; either being `high` triggers an immediate notification. The last three fields are present only on async approvals filed by `run_approved_command`.

### RunState schema

```json
{
  "lastRunAt": "<RFC3339>",
  "status": "ok | error",
  "summary": "<first 300 chars of agent output or error message>"
}
```

## Memory API (`/memory` endpoint)

`GET /memory` returns a `Dump`:

```json
{
  "domains": {
    "timeline": [ /* current snapshot */ ],
    "notifications": { /* current snapshot */ },
    "Skill States": {
      "skill_state:check_drives": { /* State */ },
      "responsibility_run:daily-patch-check": { /* RunState */ }
    }
  },
  "attrs": {
    "approval:abc123": { /* ApprovalEntry */ }
  }
}
```

`DELETE /memory` calls `store.Clear()`, which removes every entry under the root directory (all domain subdirectories and `attrs.json`). The root directory itself is preserved.

The dump is used by central-backend's agent detail page to display raw memory state. `DELETE /memory` is used by the Admin page to reset an agent's state.

## File Permissions

Files are created with mode `0644`; directories are created with mode `0755` by `os.MkdirAll`.

Under the production Linux deployment, the memory root (`/opt/agent_patches/data/memory`) is owned by `agent_patches:agent_patches` with mode `750`. Only the `agent_patches` user and root can access it. See [security.md](security.md) for the full directory permission model.

## Nil Safety

Both `DomainStore` and `AttrsStore` are nil-safe: calling any method on a nil receiver is a no-op (or returns an error, in the case of read methods). This means callers do not need nil checks after `Store.Domain` — the methods themselves handle uninitialized state cleanly.
