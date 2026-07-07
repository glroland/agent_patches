# Central Control Plane

The central control plane consists of two components: `central-backend` (Node.js/Express) and `central-ui` (React SPA). Together they provide the operator's view of the fleet, real-time status, approval handling, and fleet-wide chat.

## central-backend

### Configuration

All configuration is via environment variables. There is no config file.

| Variable | Default | Description |
|---|---|---|
| `AGENT_AUTH_TOKEN` | **required** | Bearer token sent to every agent |
| `HOST` | `0.0.0.0` | Listen address |
| `PORT` | `4000` | Listen port |
| `CORS_ORIGIN` | `http://localhost:5173` | Allowed CORS origin |
| `AGENT_POLL_INTERVAL_SECONDS` | `60` | How often the poller hits each agent |
| `AGENT_INVENTORY_FILE` | — | Path to the CSV inventory file |
| `AGENT_POLL_TIMEOUT_MS` | `3000` | Timeout for status/memory/approval calls |
| `AGENT_MESSAGE_TIMEOUT_MS` | `60000` | Timeout for `message/send` calls (full tool-use loop) |
| `EMAIL_ENABLED` | `false` | Enable email notifications |
| `EMAIL_HOST` | — | SMTP hostname |
| `EMAIL_PORT` | `587` | SMTP port |
| `EMAIL_USERNAME` | — | SMTP auth username |
| `EMAIL_PASSWORD` | — | SMTP auth password |
| `EMAIL_FROM` | — | Sender address |
| `EMAIL_TO` | — | Comma-separated recipient addresses |
| `EMAIL_TLS_MODE` | `starttls` | `starttls` \| `tls` \| `none` |
| `EMAIL_DAILY_SUMMARY_TIME` | `07:00` | Local HH:MM time for the daily summary email |
| `INTELLIGENCE_BASE_URL` | — | OpenAI-compatible API base URL. Unset disables fleet intelligence. |
| `INTELLIGENCE_API_KEY` | `none` | API key for the intelligence endpoint |
| `INTELLIGENCE_MODEL` | `gpt-4o` | Model name |
| `INTELLIGENCE_INTERVAL_MINUTES` | `30` | Re-analysis interval. `0` = run once on startup only. |
| `INTELLIGENCE_TIMEOUT_MS` | `1200000` (20m) | Timeout for intelligence API calls |
| `LOG_LEVEL` | `info` | Logging verbosity |

### Inventory

A CSV file enumerates every managed agent. Path set by `AGENT_INVENTORY_FILE`.

Columns: `fqdn`, `port`, `displayName`, `osType`, `role`, `tags`

```csv
host-a.example.com,9976,Web Server,linux,web,prod
host-b.example.com,9976,Database,linux,db,prod
```

The inventory is read at startup (and re-read on each poll cycle). It is the authoritative list of which agents to contact.

### REST API

All routes are under `/api`.

| Route | Method | Description |
|---|---|---|
| `/health` | GET | Health check — returns `{ "status": "ok" }` |
| `/api/agents` | GET | List all agents with current status |
| `/api/agents/:id` | GET | Single agent detail (timeline, responsibilities, disk trends) |
| `/api/agents/:id/memory` | GET | Raw memory dump from the agent |
| `/api/agents/:id/memory` | DELETE | Clear all memory on the agent |
| `/api/agents/:id/responsibilities` | GET | Agent's responsibilities and last run state |
| `/api/agents/:id/chat` | POST | Send a message to one agent (body: `{ "message": "..." }`) |
| `/api/approvals/:agentId/:approvalId/decision` | POST | Forward an approval decision to the agent |
| `/api/dashboard` | GET | Aggregate stats: total agents, healthy, attention count, pending approvals, open recommendations |
| `/api/intelligence` | GET | Latest fleet intelligence report |
| `/api/chat` | POST | Broadcast a message to all agents in parallel |
| `/api/summary` | GET | Daily briefing |
| `/api/issues` | GET | Open concerns (critical timeline entries) across the fleet |
| `/api/admin/memory` | DELETE | Clear all agents' memory in parallel |

Agent IDs are derived from the first segment of the FQDN (e.g. `host-a` from `host-a.example.com`).

### WebSocket (`/ws`)

A single WebSocket endpoint serves all connected UI clients.

On connection: the current fleet state is sent immediately (if the cache is populated).

On every poll cycle completion, new intelligence report, or new daily briefing, all connected clients receive a broadcast.

Message shape:

```json
{
  "type": "fleet_update",
  "agents": [ /* AgentSummary objects — id, hostname, status, statusLabel, currentTask, lastPoll, pendingApprovalCount, latestActivity */ ],
  "dashboard": {
    "stats": { "totalAgents": N, "healthyAgents": N, "attentionCount": N, "pendingApprovalCount": N, "openRecommendations": N, "hasHighRiskApproval": bool },
    "attention": [ /* agents needing attention */ ],
    "approvals": [ /* up to 4 pending approvals */ ],
    "activity": [ /* 8 most recent non-approval timeline entries across fleet */ ]
  },
  "summary": { "totalAgents": N, "attentionCount": N, "pendingApprovalCount": N, "oldestPendingApprovalTime": "...", "criticalIssueCount": N },
  "intelligence": { /* latest intelligence report or null */ },
  "briefing": { /* latest daily briefing or null */ }
}
```

A 30-second ping keeps connections alive through proxies.

### AgentClient (`services/agentClient.js`)

Wraps all HTTP communication with a single endpoint-server. One instance is created per agent per operation (not pooled).

Methods:
- `getStatus()` — `GET /status`, 3s timeout, returns `null` on any failure
- `getMemory()` — `GET /memory`, 3s timeout
- `clearMemory()` — `DELETE /memory`, 3s timeout
- `getResponsibilities()` — `GET /responsibilities`, 3s timeout
- `resolveApproval(id, decision, reason)` — `POST /approvals/:id/decision`, 3s timeout, throws on error
- `sendMessage(text, { timeoutMs })` — `POST /` (A2A JSON-RPC `message/send`), default 60s timeout

All methods pass `Authorization: Bearer <AGENT_AUTH_TOKEN>` when the token is configured. Callers treat a `null` return value as "agent offline."

### Fleet cache and poller

`services/poller.js` calls `fetchAllAgents()` on a fixed interval. `fetchAllAgents` issues `getStatus()` calls to all inventory agents in parallel and merges each response with its inventory entry (display name, role, tags, osType) into a unified agent object.

The result is stored in `fleetCache.js` (in-memory, last value only). Any component can subscribe to cache updates; the WS hub and notifier both subscribe.

The cache is populated before the first poll cycle completes only if something triggers `fetchAllAgents()` directly (e.g. a `GET /api/agents` request arrives before the first timer fires).

### Fleet intelligence (`services/intelligence.js`)

Runs on a configurable interval after a 15-second startup delay (to allow the first poll cycle to complete).

Process:
1. Serialises the current fleet state into a compact markdown document: overview counts, per-agent status and recent timeline entries, pending approvals, open concerns
2. Sends to the configured OpenAI-compatible model with a structured JSON prompt
3. Parses the response into: `headline` (string) + `recommendations` (array of up to 9 objects)

Each recommendation has: `priority` (high/medium/low), `category` (health/security/feature/configuration), `title`, `body`.

The report is stored in `intelligenceCache.js` and included in every WebSocket broadcast. Fleet intelligence is disabled when `INTELLIGENCE_BASE_URL` is not set.

### Email notifications (`services/emailer.js`, `services/notifier.js`)

Three TLS modes for SMTP:
- `starttls` (default, port 587) — STARTTLS upgrade on an initially plain connection
- `tls` (port 465) — TLS from the start
- `none` — plain SMTP

`notifier.onFleetUpdate` is called after each poll cycle. It checks for critical issues, offline agents, and pending high-risk approvals and sends targeted alerts. A daily summary email is sent once per day at `EMAIL_DAILY_SUMMARY_TIME` (local time).

### OTel tracing

Traces exported via `OTEL_EXPORTER_OTLP_ENDPOINT`. Service name: `central-backend`. Instrumented with `@opentelemetry/sdk-node`.

### Deployment

Container image built from `central-backend/Containerfile`. Helm chart at `deploy/helm/central-backend/`. ArgoCD application manifest at `deploy/argo-app.yaml`. The umbrella chart at `deploy/helm/` deploys both central components together.

## central-ui

### Pages

| Page | Route | Description |
|---|---|---|
| Dashboard | `/` | Fleet stats, attention agents, pending approvals, recent activity, embedded chat |
| Agents | `/agents` | Fleet list with status badges, last poll time, current task |
| AgentDetail | `/agents/:id` | Per-agent timeline, responsibilities, memory viewer, per-agent chat |
| Approvals | `/approvals` | Pending approval cards with approve/reject buttons and reason field |
| ActivityFeed | `/activity` | Cross-fleet timeline, all event types |
| FleetIntelligence | `/intelligence` | Latest intelligence report and recommendations |
| FleetChat | `/fleet-chat` | Broadcast a message to all enrolled agents |
| Admin | `/admin` | Clear all memory (fleet-wide), admin operations |

### Real-time updates

`useFleetSocket` hook (`src/hooks/useFleetSocket.jsx`) maintains a WebSocket connection to central-backend. On each `fleet_update` message, the hook updates shared React state that feeds all pages. No polling — the UI is entirely push-driven once the WebSocket is connected.

### API calls

`useApi` hook (`src/hooks/useApi.js`) wraps `fetch` calls to `/api/*`. Used for one-shot operations (submitting an approval decision, sending a chat message, clearing memory) where the response matters but real-time update comes via WebSocket.

`src/api/client.js` holds the base URL configuration.

### OTel tracing

Browser-side OTel is configured via `public/otel-config.js`. The file is generated at container startup by `entrypoint.sh`, which substitutes runtime environment variables (`VITE_API_URL`, `OTEL_EXPORTER_OTLP_ENDPOINT`) into the template before nginx starts. This allows the same image to run in different environments without a rebuild.

### Deployment

Container image from `central-ui/Containerfile`. Runs nginx with `nginx.conf.template`; the entrypoint performs `envsubst` to inject runtime env vars before starting nginx. Helm chart at `deploy/helm/central-ui/`. The chart exposes the UI via an OpenShift Route or Kubernetes Ingress.
