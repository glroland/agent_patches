# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
make build          # go fmt ./... && go vet ./..., then compile server (target/patches-endpoint-server) and CLI (target/patches-cli)
make test           # go test ./tests/... -v
make release        # cross-compile server + CLI for all platforms into target/<os>-<arch>/
make run            # cross-compile + start the server with ./config.yaml
make run-cli ARGS="<message>"  # build + send one task via the CLI
make deploy         # cross-compile + deploy to all inventory hosts (per-host confirmation prompts)
make run-central-backend       # local central-backend (Node) dev server
make run-central-ui            # local central-ui (React/Vite) dev server

# run a single test
go test ./tests/ -run TestPatcher_Debian -v
# package-level unit tests live beside their package
go test ./endpoint-server/loop/ ./llm-gateway/ -v
```

A `config.yaml` (copy `config.example.linux.yaml` or `config.example.windows.yaml`) must exist in the working directory before the server will start. Override with `AGENT_PATCHES_CONFIG=<path>`.

Most integration tests live in `tests/` and exercise packages cross-package; some unit tests live beside their package (e.g. `endpoint-server/loop/responsibility_test.go`, `llm-gateway/gateway_test.go`).

## Components

- `endpoint-server/` — Go agent deployed to every managed host (the bulk of the code)
- `central-backend/` — Node.js/Express fleet aggregator (REST + WebSocket for the UI)
- `central-ui/` — React SPA operator dashboard
- `llm-gateway/` — Go queuing reverse proxy in front of the shared LLM
- `llmmodel/` — one shared constant: the `DEFAULT` model sentinel the gateway rewrites
- `cli/` — one-shot A2A client
- `migrate-memory/` — one-time CLI that moves data between memory layouts on a host's memory root (run via `deploy/linux/migrate_memory.sh`, not directly against a live agent)
- `config/` — OS-default responsibilities, system prompts, and baseline-ports CSVs installed by deploy

Full docs in `docs/` (architecture, endpoint-server, central, llm-gateway, memory, security). Keep them updated when changing behavior.

## Architecture (endpoint-server)

### Request path

```
HTTP JSON-RPC → a2asrv (A2A) → executor.Executor → agent.Agent (OpenAI-compatible tool-use loop)
                                                          ↓
                                        skills (tool.Tool implementations)
                                                          ↓
                                    memory.Store (domains + attrs) / storage (tasks.jsonl audit)
```

`agent.Agent` drives an OpenAI chat-completions tool-use loop (via the shared LLM gateway) until `finish_reason != tool_calls` or MaxIter; every tool result passes through `sanitize.ToolOutput` first. Duplicate tool calls (same name+args) return cached results with a nudge to finish. Scheduled runs use `NewWithResponsibility`, which tags LLM requests with `X-Responsibility` and can cap tokens via `agent.responsibility_max_tokens`; interactive runs set `X-Priority: interactive` so the gateway prioritizes them. `storage.WrapAll` decorates every tool to append a `TaskRecord` to `tasks.jsonl` after each execution — failures are logged but never propagate.

### Background loop

`loop/loop.go` ticks on `loop.heartbeat` and fires due `Responsibility` entries (either recurring `frequency` or once-daily `time`, both offset by a per-host startup delay to stagger fleet load on the gateway). Each run gets a fresh `agent.Agent` with the responsibility's filtered tool set; open incidents from the `incidents` ledger are appended to the instruction. A registered `PreCheck` (see `lp.RegisterPreCheck` in `main.go`) runs deterministic health logic first and skips the LLM call entirely when healthy — the common case. PreCheck keys must match responsibility names in config.

### Adding a new skill (tool)

1. Create `endpoint-server/skills/<name>/<name>.go` with a typed input struct and a `New<Name>Tool(...)` constructor calling `tool.New(name, description, fn)` (schema is derived by reflection from the input struct).
2. Platform-specific code goes in sibling files with `//go:build linux` / `windows` / `!windows` tags.
3. Register it in `endpoint-server/main.go` with `registry.Register(...)`.
4. If the skill tracks health, write `skillstate.Save(mem, "<name>", ...)` so `GET /status` surfaces non-OK states; write snapshots to `mem.Domain("<name>")` for trend/baseline comparison.
5. Add tests in `tests/` following `patching_test.go` / `check_drives_test.go` patterns.

### Patching pipeline

`skills/check_for_pending_system_patches/`: the tool dry-run checks per OS (`patching.Patcher.UpdatesAvailable` — Debian: `apt-get upgrade --dry-run`; Fedora: `dnf check-update`, exit 100 = updates; Windows: PowerShell search-only), enriches with CVE data (`ListUpdates`), requests blocking HITL approval with independent **importance** (urgency, e.g. CVE severity) and **risk** (blast radius) ratings, then `Patcher.Run` applies updates and reboots if needed. The `Commander` interface abstracts `exec.Cmd`; inject a mock via `patching.NewWithCommander` in tests. `NewPreCheck` does the dry-run without the LLM.

### Approvals

Two flows (see `docs/architecture.md`): `request_approval` blocks polling AttrsStore (used by patching); `run_approved_command` files an async approval and returns immediately — the command executes when the operator approves via `POST /approvals/:id/decision`. Commands matching a standing `policy` execute immediately. Both carry `importance` and `risk`; high on either notifies immediately.

### Notifier

`utils/notifier` writes to the `notifications` memory domain — the endpoint-server itself does not send email; central-backend picks notifications up during polling and emails via its own `emailer`. A nil `*Notifier` is safe to call.

### Config defaults (applied in `config.Load`)

| Field | Default |
|---|---|
| `server.host` | `0.0.0.0` |
| `server.port` | `8080` (production configs use 9976) |
| `security.scheme` | `none` |
| `memory.root` | `./agent_memory` |
| `loop.heartbeat` | `1s` |
| `agent.model` | `DEFAULT` sentinel (gateway substitutes `GATEWAY_UPSTREAM_MODEL`) |
| `agent.request_timeout` | `6m` |
| `logging.file` | _(stderr)_ |

`config.Load` also merges `<goos>-responsibilities.yaml`, `<goos>-system-prompt.txt`, `purpose.txt`, and `<goos>-baseline-ports.csv` from the config file's directory (per-host `config.yaml` entries win over OS files).

## LLM capacity

All agents share one LLM through `llm-gateway` (bounded concurrency + queue). Token budget is tight: prefer deterministic pre-checks over new LLM calls, and give any new scheduled responsibility a `PreCheck` so healthy ticks skip the LLM entirely.

Deliberate exception: the once-daily `system-insights` responsibility has no pre-check on purpose — it is the only run guaranteed to put a *healthy* system in front of the model for open-ended observations and recommendations (pre-checks otherwise mean the LLM only ever sees broken systems). Do not add a pre-check to it or scope it to detected problems.
