# LLM Gateway

`llm-gateway/` is a Go queuing reverse proxy that sits between every endpoint-server agent and the shared upstream LLM (any OpenAI-compatible server, including Ollama). The fleet's agents far outnumber the LLM's capacity; the gateway turns that contention into bounded, observable queuing instead of upstream overload.

## What it does

- **Bounded concurrency** — a fixed-size worker pool (`GATEWAY_MAX_CONCURRENCY`, default 2) limits simultaneous upstream requests.
- **Two FIFO queues** — a priority queue for interactive (UI-initiated) requests and a normal queue for scheduled background work. The dispatcher acquires a concurrency slot *before* dequeuing and always drains the priority queue first, so an interactive request that arrives while waiting for capacity beats background work already queued.
- **Fast-fail on saturation** — when the applicable queue is full the gateway returns `429` immediately with `Retry-After: 5` rather than blocking the caller.
- **Default-model substitution** — when a request body's `model` is the `llmmodel.Default` sentinel (`"DEFAULT"`, the endpoint-server default when `agent.model` is unset), the gateway rewrites it to `GATEWAY_UPSTREAM_MODEL`. The model for the whole fleet is thus configured in exactly one place; agents that set an explicit `agent.model` pass through untouched.
- **Token/request statistics** — the response body is teed into a bounded capture buffer (256 KB) and token usage extracted (OpenAI JSON, SSE streams, and Ollama formats). Stats are tracked per endpoint host and per responsibility over a 25-hour sliding window, optionally persisted to `GATEWAY_DATA_FILE` (atomic temp+rename) so they survive pod restarts.
- **Ghost tracking** — requests whose client disconnects while queued are marked cancelled (the upstream call is skipped when dispatched) and counted as "ghosts" so `/stats` reports effective queue depths.
- **Connection error tracking** — abandoned/timed out/cancelled/prematurely closed connections are counted gateway-wide over the same 25-hour sliding window (last hour / total surfaced via `/stats`), split by direction: **incoming** (the endpoint-server client gave up — disconnected before dispatch, cancelled mid-flight, or dropped mid-response) and **outgoing** (the gateway failed to reach or got no timely response from the upstream LLM — timeout or network error). central-ui shows the combined per-hour count as a box in the Activity page's overview bar.
- **Streaming passthrough** — response bytes are flushed to the caller in real time; capture never delays the stream.

## Request headers from endpoint-server

The agent's HTTP transport (`a2a/agent/agent.go`) sets three headers on every LLM call:

| Header | Value | Gateway use |
|---|---|---|
| `X-Agent-Name` | agent display name | per-endpoint stats attribution |
| `X-Responsibility` | scheduled responsibility name (empty for ad-hoc runs) | per-responsibility stats attribution |
| `X-Priority` | `interactive` for operator-initiated runs | routes to the priority queue |

endpoint-server sets `X-Priority: interactive` on chat requests arriving through its own A2A endpoint (`interactivePriorityMiddleware` in `main.go`), so operator chat stays responsive even when scheduled work has the normal queue backed up.

## HTTP API

| Endpoint | Method | Auth | Description |
|---|---|---|---|
| `/health` | GET | none (K8s probes) | Queue depths, capacities, active requests, ghost count |
| `/stats` | GET | bearer | Full stats: per-endpoint and per-responsibility token/request counts (last hour / last day / total), queue state, upstream info, incoming/outgoing connection error counts (last hour / total) |
| `/stats` | DELETE | bearer | Reset all accumulated statistics (also overwrites the persisted file) |
| `/pending` | GET | bearer | Live registry of queued + in-flight requests: host, agent, responsibility, extracted prompt, age, priority flag |
| _anything else_ | any | bearer | Queued and proxied verbatim to the upstream |

central-backend proxies `GET /api/gateway/stats` and `GET /api/gateway/pending` to these endpoints for display in the UI.

## Configuration (environment variables)

| Variable | Default | Description |
|---|---|---|
| `GATEWAY_UPSTREAM_URL` | **required** | Base URL of the OpenAI-compatible upstream |
| `GATEWAY_UPSTREAM_MODEL` | — | Model substituted for the `DEFAULT` sentinel; also shown in `/stats` |
| `GATEWAY_LISTEN_ADDR` | `:8080` | Listen address |
| `GATEWAY_MAX_CONCURRENCY` | `2` | Simultaneous upstream requests |
| `GATEWAY_MAX_QUEUE_DEPTH` | `50` | Normal (background) queue capacity |
| `GATEWAY_PRIORITY_QUEUE_DEPTH` | `10` | Interactive queue capacity |
| `GATEWAY_REQUEST_TIMEOUT` | `5m` | Per-request upstream timeout (agents' `agent.request_timeout` should sit just above this) |
| `GATEWAY_AUTH_TOKEN` | — | Bearer token; empty disables auth (startup warning) |
| `GATEWAY_DATA_FILE` | — | JSON stats persistence path (e.g. a PVC mount); empty disables |
| `GATEWAY_SAVE_INTERVAL` | `60s` | Stats flush interval; a final save runs on SIGTERM |

## Deployment

Helm subchart at `deploy/helm/llm-gateway/`, deployed by the umbrella chart at `deploy/helm/` alongside central-backend and central-ui. `/health` stays unauthenticated for liveness/readiness probes. On shutdown the server drains in-flight requests for up to 30 seconds.
