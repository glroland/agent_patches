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

Retention policy (enforced on every write):
- One snapshot per 5-minute age bucket for the last 60 minutes
- Anything older than 60 minutes is deleted
- Within a bucket, the newest snapshot is kept and older ones are deleted

This means the domain stores up to 12 snapshots in normal operation (one per 5-minute bucket over 60 minutes).

### Read semantics

- `ReadCurrent(v)` — deserialises the newest file into `v`. Returns an error if no files exist.
- `ReadHistory()` — returns all retained snapshots as `[]Snapshot` sorted oldest-first. Each `Snapshot` has a `Timestamp` and a `json.RawMessage`.

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
| `check_interactive_logins` | login skill | — | Login session snapshot |

Skills that track trends call `ReadHistory()` on their own domain to compare current values against the 60-minute history window. This is how disk growth rate and SMART attribute drift are detected.

## Attrs Keys in Production

| Key pattern | Type | Written by | Read by | Description |
|---|---|---|---|---|
| `approval:<uuid>` | `ApprovalEntry` | `request_approval` | `approvalapi`, `request_approval` (poll) | HITL approval state. Status transitions: `pending` → `approved \| rejected \| timed_out \| cancelled`. |
| `responsibility_run:<name>` | `RunState` | `loop.execute` | `GET /responsibilities` | Last run outcome: `{ lastRunAt, status, summary }` |
| `last_patched_at` | RFC3339 string | `check_for_pending_system_patches` | `GET /status` | Timestamp of the last successful OS update |
| `disk_trends` | JSON object | `check_drives` | `GET /status` | Serialized disk trend data for the UI |
| `smart_trends` | JSON object | `check_drives` | `GET /status` | Serialized SMART attribute trends for the UI |
| `skillstate:<skill>` | `HealthState` | `check_*`, `analyze_*` skills | `GET /status` | Health state from the last skill run: `{ health: ok\|warning\|critical, summary, time }`. Non-OK states are merged into the `/status` timeline even if `report_findings` was not called. |

### ApprovalEntry schema

```json
{
  "id": "<uuid>",
  "title": "...",
  "detail": "...",
  "proposedAction": "apt-get upgrade -y",
  "risk": "low | medium | high",
  "status": "pending | approved | rejected | timed_out | cancelled",
  "requestedAt": "<RFC3339>",
  "decidedAt": "<RFC3339 or null>",
  "reason": "<operator comment or empty>"
}
```

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
    "notifications": { /* current snapshot */ }
  },
  "attrs": {
    "approval:abc123": { /* ApprovalEntry */ },
    "responsibility_run:daily-patch-check": { /* RunState */ }
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
