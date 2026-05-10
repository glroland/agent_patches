# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
make build          # compile server (target/patches-server) and CLI (target/patches-cli)
make test           # go test ./tests/... -v
make lint           # go fmt ./... && go vet ./...
make run            # build + start the server
make run-cli ARGS="<message>"  # build + send one task via the CLI

# build or test a single package
go build ./server/scheduler/
go test ./server/scheduler/ -run TestNextWake
go test ./tests/ -run TestPatcher_Debian
```

A `config.yaml` (copied from `config.example.yaml`) must exist in the working directory before the server will start.  Override with `AGENT_PATCHES_CONFIG=<path>`.

All integration tests live in `tests/`; package-level unit tests live beside their package (e.g. `server/scheduler/scheduler_test.go`).

## Architecture

### Request path

```
HTTP JSON-RPC → a2asrv.Handler → executor.Executor → agent.Agent (Claude tool-use loop)
                                                            ↓
                                               tasks (BetaTool implementations)
                                                            ↓
                                               storage.Store (JSONL audit log)
```

`executor.Executor` satisfies the A2A `AgentExecutor` interface by extracting plain text from the incoming A2A message and calling `agent.Agent.Run`.  
`agent.Agent` drives the Anthropic SDK `ToolRunner` loop until the model stops calling tools, then returns the final text.  
`storage.WrapAll` wraps every tool with a `storedTool` decorator that appends a `TaskRecord` to `tasks.jsonl` after each execution — failures are logged but never propagate.

### Adding a new tool

1. Create `server/tasks/<name>.go` defining an input struct and a `New<Name>Tool(...)` constructor that calls `toolrunner.NewBetaToolFromJSONSchema`.
2. Register it in `server/main.go` with `registry.Register(tool)`.
3. Add tests in `tests/` following the patterns in `patching_test.go`.

The tool receives a `context.Context` and its typed input; return a `BetaToolResultBlockParamContentUnion` via the `textResult` helper.

### Adding a new daily task

Add a method on `scheduler.Scheduler` and call it from `scheduler.run`.  Add a corresponding `Enabled bool` field under `DailyTasksSettings` in `server/config/config.go` and gate the method on that flag.

### Patching pipeline

`patching.Patcher` has two public methods called in sequence by `tasks.NewPatchTool`:

1. `UpdatesAvailable(ctx)` — dry-run check per OS (no changes made).  Returns `(bool, summary, error)`.  Debian refreshes the package index then runs `apt-get upgrade --dry-run`; Fedora uses `dnf check-update` (exit 100 = updates); Windows uses PowerShell search-only.
2. `Run(ctx)` — applies updates, checks reboot requirement, reboots if needed.

The `Commander` interface abstracts `exec.Cmd`; inject a `mockCmdr` in tests via `patching.NewWithCommander`.

### Notifier

`notifier.New(&cfg.Notifier)` returns a `*Notifier` that fans out to all enabled sinks.  Currently only `emailSink` is implemented (three TLS modes: `starttls`, `tls`, `none`).  A nil `*Notifier` is safe to call — `Notify` is a no-op.  New sinks implement the `Sink` interface (`Send(ctx, subject, body) error`) and are enabled in `notifier.New`.

### Daily task scheduler

`scheduler.New(&cfg.DailyTasks, notify).Start(ctx)` launches a goroutine that:
- runs all enabled tasks immediately on startup
- then sleeps until the next wall-clock occurrence of `wake_time` (HH:MM local time), recalculating on every loop iteration to handle DST correctly

The `nextWakeFunc` field is overridable in tests to inject a short duration instead of a real 24 h wait.

### Config defaults (applied in `config.Load`)

| Field | Default |
|---|---|
| `server.host` | `0.0.0.0` |
| `server.port` | `8080` |
| `security.scheme` | `none` |
| `daily_tasks.wake_time` | `00:00` |
