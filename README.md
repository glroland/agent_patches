# agent_patches

An AI-powered server administration agent that applies OS patches, monitors
patch availability, and delivers notifications. Built on the
[A2A protocol](https://github.com/a2aproject/a2a-go) with a
[Claude](https://www.anthropic.com/claude) tool-use loop.

## Features

- **Agent-driven patching** — a Claude-backed agent accepts natural-language
  requests and runs the appropriate OS update commands (Debian/Ubuntu,
  Fedora/RHEL/Rocky, Windows)
- **Pre-flight update check** — before applying patches the agent checks
  whether updates are actually available; if none are found it stops without
  touching the system or sending notifications
- **Notifications** — a pluggable notifier sends email alerts before patching
  starts, on completion, and on failure; a separate alert fires when the daily
  check finds pending updates
- **Daily task loop** — a background goroutine wakes once per day at a
  configured wall-clock time, checks for available updates, and notifies if
  any are found; the loop always runs and individual tasks are
  individually enabled or disabled in config
- **Bearer-token security** — optional `Authorization: Bearer <token>` guard
  on every JSON-RPC request
- **A2A agent card** — standard `/.well-known/agent.json` endpoint for
  capability discovery

## Quick start

```bash
cp config.example.yaml config.yaml
# edit config.yaml — set your Claude model, SMTP credentials, bearer token, etc.
make build
./target/patches-server
```

The server exposes a JSON-RPC endpoint on `0.0.0.0:8080` by default.

## Configuration

All configuration lives in a single YAML file (default `./config.yaml`).
Override the path with the `AGENT_PATCHES_CONFIG` environment variable.

```yaml
agent:
  model: claude-opus-4-7   # Claude model to use
  max_tokens: 4096
  max_iterations: 10
  system_prompt: |
    You are agent_patches, an AI agent that handles server administration tasks.

logging:
  level: info              # debug | info | warn | error
  # file: /var/log/agent_patches.log  # omit to log to stderr

storage:
  tasks_file: tasks.jsonl  # persists in-progress task state

server:
  host: 0.0.0.0
  port: 8080
  # public_url: http://myserver.example.com:8080
  # URL embedded in the agent card. Defaults to http://<hostname>:<port>.
  # Set this when clients connect via a hostname that differs from os.Hostname().

# security.scheme: "none" | "bearer"
security:
  scheme: bearer
  token: change-me

# daily_tasks — background loop that wakes once per day.
# The goroutine always starts; each task is gated by its own enabled flag.
daily_tasks:
  wake_time: "00:00"       # HH:MM in local time (default: midnight)
  patch_check:
    enabled: true          # check for OS updates and notify when found

# notifier — event sinks for patch lifecycle and daily-check alerts.
notifier:
  email:
    enabled: false
    host: smtp.example.com
    port: 587
    username: alerts@example.com
    password: change-me
    from: alerts@example.com
    to:
      - admin@example.com
    # tls_mode: "starttls" (default) | "tls" (port 465) | "none" (local relay)
    tls_mode: starttls
```

### TLS modes for email

| `tls_mode` | Transport | Typical port |
|---|---|---|
| `starttls` (default) | Plain TCP upgraded via STARTTLS | 587 |
| `tls` | Implicit TLS (SMTPS) from the start | 465 |
| `none` | No encryption — local/relay servers only | 25 |

## Notifications

The notifier fires on the following events:

| Event | Subject |
|---|---|
| Patch starting | `[hostname] Patch Starting` |
| Patch completed | `[hostname] Patch Complete` |
| Patch failed | `[hostname] Patch Failed` |
| Daily check found updates | `[hostname] Updates Available` |

Notifications are **not** sent when no updates are available.

## Daily task loop

The background goroutine starts with the server and wakes at the time
specified by `daily_tasks.wake_time` (local timezone). On each wake it:

1. Runs `checkPatches` (if `patch_check.enabled: true`) — calls the OS
   package manager in dry-run mode to list pending updates, then sends a
   notification if any are found.

Additional daily tasks will be added here as the agent gains new capabilities.
The goroutine runs regardless of whether any tasks are enabled.

## OS support

| OS family | Detection | Update command | Reboot check |
|---|---|---|---|
| Debian / Ubuntu | `/etc/os-release` `ID`/`ID_LIKE` | `apt-get update && apt-get upgrade` | `/var/run/reboot-required` |
| Fedora / RHEL / Rocky | `/etc/os-release` | `dnf update` (falls back to `yum`) | `needs-restarting -r` |
| Windows | `runtime.GOOS` | PowerShell + Windows Update COM API | Registry key |

## Building

```bash
make build          # builds server and CLI into ./target/
make test           # runs the full test suite
```

## Project layout

```
server/
  agent/        Claude tool-use loop
  config/       YAML config loader and types
  executor/     A2A task executor
  logger/       slog setup
  notifier/     event sinks (email; extensible)
  patching/     OS detection, update checking, patch execution
  scheduler/    daily background task loop
  storage/      task persistence (JSONL)
  tasks/        agent tool definitions (hello, patch)
cli/
  client/       A2A JSON-RPC client
tests/          integration and unit tests
```
