# Architecture

agent_patches is a fleet management system made up of three components: an endpoint agent that runs on each managed host, a central backend that aggregates fleet state, and a web UI for operator control.

## Components

### endpoint-server

A Go binary deployed to every managed host. It exposes an HTTP server (default port 9976) that serves both the A2A JSON-RPC protocol and several plain HTTP APIs.

Responsibilities:
- Receives tasks from operators and central-backend via the A2A `message/send` JSON-RPC method
- Runs a background loop that fires scheduled responsibilities autonomously
- Monitors login events (successful and failed) in real time
- Exposes agent health, timeline, and memory data for polling

See [endpoint-server.md](endpoint-server.md) for full detail.

### central-backend

A Node.js/Express server that runs centrally (typically on Kubernetes/OpenShift). It:
- Maintains a CSV inventory of enrolled agents
- Polls every agent's `/status` endpoint on a configurable interval (default 60s)
- Aggregates fleet state into a cache
- Runs fleet intelligence analysis using an OpenAI-compatible LLM
- Pushes real-time updates to UI clients via WebSocket (`/ws`)
- Forwards approval decisions from the UI to the relevant agent
- Sends email notifications for fleet-wide events

See [central.md](central.md) for full detail.

### central-ui

A React SPA served by nginx. Connects to central-backend via HTTP REST and WebSocket. Pages: Dashboard, Agents, AgentDetail, Approvals, ActivityFeed, FleetIntelligence, FleetChat, Admin.

See [central.md](central.md) for full detail.

## System Topology

```
┌─────────────────────────────────────────────────────────────────┐
│  Operator browser                                               │
│  central-ui (React SPA, nginx)                                  │
│    HTTP REST ──────────────────────────────────────────────┐    │
│    WebSocket (/ws) ─────────────────────────────────────┐  │    │
└────────────────────────────────────────────────────────────────┘
                                                           │  │
                                              WebSocket    │  │ REST /api/*
                                                           ▼  ▼
                               ┌────────────────────────────────────┐
                               │  central-backend (Node.js/Express) │
                               │                                    │
                               │  CSV inventory                     │
                               │  fleet cache (in-memory)           │
                               │  intelligence (LLM analysis)       │
                               │  poller (60s interval)             │
                               │  WS hub                            │
                               │  emailer                           │
                               └──────────────┬─────────────────────┘
                                              │
                    ┌─────────────────────────┼────────────────────────┐
                    │  GET /status (poll)     │  POST / (A2A chat)     │
                    │  POST /approvals/       │  DELETE /memory        │
                    ▼                         ▼                        ▼
          ┌──────────────────┐    ┌──────────────────┐    ┌──────────────────┐
          │  endpoint-server │    │  endpoint-server │    │  endpoint-server │
          │  host-a:9976     │    │  host-b:9976     │    │  host-c:9976     │
          │                  │    │                  │    │                  │
          │  A2A JSON-RPC    │    │  A2A JSON-RPC    │    │  A2A JSON-RPC    │
          │  background loop │    │  background loop │    │  background loop │
          │  memory store    │    │  memory store    │    │  memory store    │
          └──────────────────┘    └──────────────────┘    └──────────────────┘

CLI tool: patches-cli ──────────────────────────────────────────────────────►
          (one-shot A2A message/send to any endpoint-server)
```

## Data Flows

### Scheduled responsibility

```
Loop tick
  └─► Responsibility.Due?
        └─► goroutine: agent.Agent.Run(instruction)
              └─► LLM tool-use loop
                    ├─► tool.Execute → result → sanitize.ToolOutput → append to messages
                    └─► (repeat until finish_reason != tool_calls)
                          └─► report_findings writes to memory timeline domain
                                └─► persisted at <memory.root>/timeline/<nanoseconds>.json
```

### On-demand message (operator chat or fleet chat)

```
central-ui (POST /api/agents/:id/chat)
  └─► central-backend → POST http://<agent>:<port>/ (A2A JSON-RPC message/send)
        └─► executor.Execute → agent.Agent.Run(text)
              └─► LLM tool-use loop (same as above)
                    └─► text response returned synchronously
```

### Status polling

```
poller (every AGENT_POLL_INTERVAL_SECONDS)
  └─► parallel: GET /status on every enrolled agent
        └─► fleet cache updated
              ├─► WebSocket broadcast to all connected UI clients
              └─► notifier.onFleetUpdate (email on critical events)
```

### HITL approval flow

```
Agent calls request_approval tool
  └─► writes ApprovalEntry to AttrsStore (approval:<uuid>)
        └─► writes TimelineEntry (type=approval, status=pending) to timeline domain
              └─► high-risk: notifier.Notify fires immediately (email)
                    └─► polls AttrsStore every 5s (up to 24h)

Operator sees pending approval in central-ui (via WS broadcast)
  └─► submits decision (approve/reject)
        └─► central-backend POST /api/approvals/:agentId/:approvalId/decision
              └─► forwards to agent: POST /approvals/:id/decision
                    └─► handler updates ApprovalEntry status in AttrsStore
                          └─► polling loop unblocks, returns decision to agent
```

## HTTP API Surface (endpoint-server)

| Endpoint | Method | Description |
|---|---|---|
| `/.well-known/agent.json` | GET | A2A agent card |
| `/` | POST | A2A JSON-RPC (`message/send`) |
| `/status` | GET | Health, timeline, trends |
| `/memory` | GET | Full memory dump |
| `/memory` | DELETE | Clear all memory |
| `/approvals/` | GET | List pending approvals |
| `/approvals/:id/decision` | POST | Submit approve/reject |
| `/responsibilities` | GET | Responsibilities + last run state |

All endpoints except `/.well-known/agent.json` and `/` require `Authorization: Bearer <token>` when `security.scheme = bearer`.

## Deployment

### endpoint-server (Linux)

Deployed via `deploy/linux/deploy.sh` (Ansible-driven). The binary is cross-compiled for each target platform and installed to `/opt/agent_patches/bin/`. Runs as a systemd service (`deploy/linux/agent_patches.service`) under the `agent_patches` system user. See [security.md](security.md) for the full privilege model.

### endpoint-server (Windows)

Deploy script at `deploy/linux/deploy.sh` includes a PowerShell section that SCPs the binary and config to the target via SSH and registers a Windows Service via `sc.exe`. Runs as the `agent_patches` local user account.

### central-backend and central-ui (Kubernetes/OpenShift)

Helm charts at `deploy/helm/central-backend/` and `deploy/helm/central-ui/`. Top-level umbrella chart at `deploy/helm/`. ArgoCD application manifest at `deploy/argo-app.yaml`. Both components have `Containerfile`s for container image builds.

### CLI

`cli/` contains a Go CLI (`patches-cli`) that sends a single text message to an endpoint-server via A2A `message/send` and prints the response. Useful for one-off commands without the full central-backend stack.

```
make run-cli ARGS="check disk usage and report findings"
```

## Configuration

Each endpoint-server reads a YAML file at `config.yaml` (or `$AGENT_PATCHES_CONFIG`). central-backend is configured entirely via environment variables. See [endpoint-server.md](endpoint-server.md) and [central.md](central.md) for the full field reference.

## Observability

All three components export OpenTelemetry traces via `OTEL_EXPORTER_OTLP_ENDPOINT`. endpoint-server service names default to `endpoint-server-<FQDN>`; central-backend uses `central-backend`; central-ui uses browser OTel. Traces cover LLM inference calls, individual tool executions, and responsibility runs.
