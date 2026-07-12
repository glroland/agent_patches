# agent_patches

An AI-powered fleet management system for servers. An autonomous LLM agent
runs on every managed host — monitoring health, applying OS patches behind a
human-in-the-loop approval gate, watching for security drift, and reporting
findings to a central dashboard. Built on the
[A2A protocol](https://github.com/a2aproject/a2a-go) with an
OpenAI-compatible tool-use loop.

## Components

| Component | Language | Runs on | Purpose |
|---|---|---|---|
| [endpoint-server](docs/endpoint-server.md) | Go | every managed host | The agent: A2A JSON-RPC server, scheduled responsibilities, tool-use loop, login/network monitoring, durable memory |
| [central-backend](docs/central.md) | Node.js/Express | Kubernetes/OpenShift | Fleet aggregation: polls all agents, WebSocket push to the UI, fleet intelligence (LLM analysis), approvals forwarding, email alerts |
| [central-ui](docs/central.md) | React SPA | Kubernetes/OpenShift | Operator dashboard: fleet status, approvals, per-agent chat, activity feed, intelligence reports |
| [llm-gateway](docs/llm-gateway.md) | Go | Kubernetes/OpenShift | Queuing reverse proxy in front of the shared LLM: bounded concurrency, interactive-priority queue, per-agent token stats |
| cli | Go | anywhere | `patches-cli` — one-shot A2A `message/send` to any endpoint-server |

See [docs/architecture.md](docs/architecture.md) for the full system topology
and data flows.

## Features

- **Autonomous responsibilities** — each agent runs a schedule of health
  checks (disks, CPU, memory, network, temperature, containers, NFS, security
  posture, pending patches) driven by an LLM tool-use loop. Deterministic
  pre-checks skip the LLM call entirely when everything is healthy.
- **Human-in-the-loop patching** — the agent analyses pending updates
  (including CVE severity), files an approval with independent *importance*
  and *risk* ratings, and only applies patches after an operator approves.
- **Standing approval policies** — operators can promote frequently-approved
  commands to regex-matched policies that execute without a fresh approval.
- **Security monitoring** — real-time login and network-connection monitors
  compare activity against each host's own learned baseline; drift in
  listening ports, users, sudoers, or authorized_keys is flagged.
- **Incident ledger** — fingerprinted, deduplicated incidents survive across
  runs so the agent tracks ongoing problems instead of re-reporting them.
- **Durable agent memory** — tiered snapshot retention (5-minute resolution
  for an hour, hourly for a week, daily for 90 days) lets the agent compare
  current readings against the host's own history.
- **Fleet dashboard** — real-time WebSocket-driven UI with fleet-wide chat,
  approvals, activity feed, and periodic LLM-generated fleet intelligence.
- **Prompt-injection defense** — every tool output is sanitized (control
  characters, injection phrases, truncation) before reaching the model.
  See [docs/security.md](docs/security.md).

## Quick start (single agent)

```bash
cp config.example.linux.yaml config.yaml
# edit config.yaml — LLM base_url/model, bearer token, responsibilities, etc.
make build
./target/patches-endpoint-server
```

The agent serves A2A JSON-RPC and its HTTP APIs on `0.0.0.0:8080` by default
(production deploys typically configure port 9976). Override the config path
with `AGENT_PATCHES_CONFIG=<path>`.

Send it a task:

```bash
make run-cli ARGS="check disk usage and report findings"
```

## Building and testing

```bash
make build          # go fmt + go vet, then build server and CLI into ./target/
make test           # full test suite (integration tests in tests/)
make release        # cross-compile server + CLI for linux/darwin/windows, amd64/arm64
make deploy         # deploy to all hosts in inventory.csv (Ansible/SSH driven)
make run-central-backend   # local central-backend dev server
make run-central-ui        # local central-ui dev server (Vite)
make help           # list all targets
```

## Deployment

- **endpoint-server** — `deploy/linux/deploy.sh` installs the binary, config,
  OS-specific responsibilities/system-prompt/baseline-ports files, and a
  systemd service (Linux) or Windows service on each host in `inventory.csv`.
  Runs as a locked-down `agent_patches` service account; see
  [docs/security.md](docs/security.md) for the privilege model.
- **central components + llm-gateway** — Helm umbrella chart at
  `deploy/helm/` (subcharts: `central-backend`, `central-ui`, `llm-gateway`),
  ArgoCD app manifest at `deploy/argo-app.yaml`.

## Project layout

```
endpoint-server/     the per-host agent
  a2a/               agent loop, executor, tool interface, registry
  skills/            tool implementations (check_*, analyze_*, run_*, ...)
  loop/              responsibility scheduler + pre-checks
  memory/            tiered snapshot store + key-value attrs store
  *api/              plain HTTP endpoints (/status, /memory, /approvals, ...)
  loginmonitor/      interactive/failed login monitoring + baselines
  connmonitor/       network connection monitoring + baselines
  incidents/         fingerprinted incident ledger
  policy/            standing approval policies
  status/            GET /status aggregation, timeline, summarizer
  utils/             config, logger, notifier, sanitize, storage, tracing
central-backend/     Node.js fleet aggregator + REST/WS API
central-ui/          React operator dashboard
llm-gateway/         queuing LLM reverse proxy
llmmodel/            shared model-name sentinel
cli/                 patches-cli (one-shot A2A client)
migrate-memory/      one-time memory-layout migration tool (see deploy/linux/migrate_memory.sh)
config/              OS default responsibilities, system prompts, baseline ports
deploy/              deploy scripts, systemd unit, Helm charts, ArgoCD app
docs/                architecture, endpoint-server, central, llm-gateway,
                     memory, security
tests/               integration tests (run with make test)
```

## Documentation

- [Architecture](docs/architecture.md) — components, topology, data flows
- [Endpoint server](docs/endpoint-server.md) — APIs, tools, loop, config
- [Central control plane](docs/central.md) — backend, UI, REST/WS API
- [LLM gateway](docs/llm-gateway.md) — queuing, priorities, token stats
- [Memory system](docs/memory.md) — domains, attrs, retention
- [Security](docs/security.md) — auth, privilege separation, HITL, sanitization
