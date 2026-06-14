// Merges the read-only CSV inventory with each agent's live GET /status
// response into the full fleet view consumed by the other services/controllers.

import * as inventory from './inventory.js';
import { AgentClient } from './agentClient.js';

const STATUS_META = {
  active: { label: 'Active', description: 'Currently working on a task' },
  idle: { label: 'Idle', description: 'Healthy, nothing pending' },
  attention: { label: 'Needs attention', description: 'Flagged something for review' },
  offline: { label: 'Offline', description: 'Not responding to polls' },
};

// Short-TTL cache so a single page load (which may hit dashboard, summary,
// and agents endpoints) doesn't fan out multiple /status calls per agent.
const STATUS_CACHE_TTL_MS = 5000;
const statusCache = new Map(); // id -> { data, fetchedAt }

// Exposed for tests only, so each test can start from a clean cache.
export function _clearStatusCacheForTests() {
  statusCache.clear();
}

function shortHost(fqdn) {
  return fqdn.split('.')[0].toLowerCase();
}

async function fetchStatus(inventoryAgent, id) {
  const cached = statusCache.get(id);
  if (cached && Date.now() - cached.fetchedAt < STATUS_CACHE_TTL_MS) {
    return cached.data;
  }
  const client = new AgentClient({ fqdn: inventoryAgent.fqdn, port: inventoryAgent.port });
  const data = await client.getStatus();
  statusCache.set(id, { data, fetchedAt: Date.now() });
  return data;
}

async function toFleetAgent(inventoryAgent) {
  const id = shortHost(inventoryAgent.fqdn);
  const data = await fetchStatus(inventoryAgent, id);

  const agentInfo = data?.agent ?? {};
  const statusBlock = data?.status ?? { state: 'offline', lastPoll: null, currentTask: null };
  const timeline = data?.timeline ?? [];
  const state = data ? statusBlock.state : 'offline';
  const statusMeta = STATUS_META[state] ?? STATUS_META.offline;

  return {
    id,
    hostname: inventoryAgent.fqdn,
    displayName: inventoryAgent.displayName,
    port: inventoryAgent.port,
    osType: inventoryAgent.osType,
    os: agentInfo.os || inventoryAgent.osType,
    role: inventoryAgent.role || 'Endpoint agent',
    tags: inventoryAgent.tags || [],
    status: state,
    statusLabel: statusMeta.label,
    statusDescription: statusMeta.description,
    lastPoll: statusBlock.lastPoll ?? null,
    currentTask: statusBlock.currentTask ?? null,
    timeline,
  };
}

// Returns the full fleet: inventory rows merged with live agent status.
export async function listFleet() {
  return Promise.all(inventory.listAgents().map(toFleetAgent));
}

// Returns a single fleet agent by id (short hostname), or undefined if not found.
export async function getFleetAgent(id) {
  const agents = await listFleet();
  return agents.find((agent) => agent.id === id);
}
