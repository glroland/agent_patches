// Merges the read-only CSV inventory with each agent's live GET /status
// response into the full fleet view consumed by the other services/controllers.

import * as inventory from './inventory.js';
import { AgentClient } from './agentClient.js';
import { config } from '../config/index.js';
import { getFleet } from './fleetCache.js';

const STATUS_META = {
  active: { label: 'Active', description: 'Currently working on a task' },
  idle: { label: 'Idle', description: 'Healthy, nothing pending' },
  attention: { label: 'Needs attention', description: 'Flagged something for review' },
  offline: { label: 'Offline', description: 'Not responding to polls' },
};

function shortHost(fqdn) {
  return fqdn.split('.')[0].toLowerCase();
}

async function toFleetAgent(inventoryAgent) {
  const id = shortHost(inventoryAgent.fqdn);
  const client = new AgentClient({
    fqdn: inventoryAgent.fqdn,
    port: inventoryAgent.port,
    authToken: config.agents.authToken,
  });
  const data = await client.getStatus();

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

// Polls every enrolled agent in parallel. Called by the poller; also used as
// a fallback by listFleet() before the first poll cycle completes.
export async function fetchAllAgents() {
  return Promise.all(inventory.listAgents().map(toFleetAgent));
}

// Returns the full fleet from the cache populated by the poller. Falls back
// to a live fetch if the cache has not yet been populated (e.g. first request
// arrives before the first poll cycle finishes).
export async function listFleet() {
  return getFleet() ?? fetchAllAgents();
}

// Returns a single fleet agent by id (short hostname), or undefined if not found.
export async function getFleetAgent(id) {
  const agents = await listFleet();
  return agents.find((agent) => agent.id === id);
}

// Returns the agent's GET /memory data, or undefined if not in inventory, or
// null if the agent is unreachable.
export async function getAgentMemory(id) {
  const inventoryAgent = inventory.listAgents().find((agent) => shortHost(agent.fqdn) === id);
  if (!inventoryAgent) {
    return undefined;
  }

  const client = new AgentClient({
    fqdn: inventoryAgent.fqdn,
    port: inventoryAgent.port,
    authToken: config.agents.authToken,
  });
  return client.getMemory();
}

export async function sendAgentMessage(id, text) {
  const inventoryAgent = inventory.listAgents().find((agent) => shortHost(agent.fqdn) === id);
  if (!inventoryAgent) {
    return undefined;
  }

  const client = new AgentClient({
    fqdn: inventoryAgent.fqdn,
    port: inventoryAgent.port,
    authToken: config.agents.authToken,
  });
  return client.sendMessage(text);
}

export async function resolveApproval(agentId, approvalId, decision, reason = '') {
  const inventoryAgent = inventory.listAgents().find((agent) => shortHost(agent.fqdn) === agentId);
  if (!inventoryAgent) {
    throw new Error(`agent ${agentId} not found in inventory`);
  }
  const client = new AgentClient({
    fqdn: inventoryAgent.fqdn,
    port: inventoryAgent.port,
    authToken: config.agents.authToken,
  });
  return client.resolveApproval(approvalId, decision, reason);
}

// Sends DELETE /memory to a single agent. Returns undefined if not in inventory,
// null if unreachable, or the agent's JSON response on success.
export async function clearAgentMemory(id) {
  const inventoryAgent = inventory.listAgents().find((agent) => shortHost(agent.fqdn) === id);
  if (!inventoryAgent) return undefined;
  const client = new AgentClient({
    fqdn: inventoryAgent.fqdn,
    port: inventoryAgent.port,
    authToken: config.agents.authToken,
  });
  return client.clearMemory();
}

// Sends DELETE /memory to every enrolled agent in parallel. Returns an array
// of { id, hostname, ok, error? } results.
export async function clearAllAgentsMemory() {
  return Promise.all(
    inventory.listAgents().map(async (inventoryAgent) => {
      const id = shortHost(inventoryAgent.fqdn);
      const client = new AgentClient({
        fqdn: inventoryAgent.fqdn,
        port: inventoryAgent.port,
        authToken: config.agents.authToken,
      });
      const result = await client.clearMemory();
      return { id, hostname: inventoryAgent.fqdn, ok: result !== null, error: result === null ? 'unreachable' : undefined };
    })
  );
}

export async function broadcastMessage(text) {
  return Promise.all(
    inventory.listAgents().map(async (inventoryAgent) => {
      const id = shortHost(inventoryAgent.fqdn);
      const client = new AgentClient({
        fqdn: inventoryAgent.fqdn,
        port: inventoryAgent.port,
        authToken: config.agents.authToken,
      });
      try {
        const reply = await client.sendMessage(text);
        return { id, hostname: inventoryAgent.fqdn, displayName: inventoryAgent.displayName, reply };
      } catch (err) {
        return { id, hostname: inventoryAgent.fqdn, displayName: inventoryAgent.displayName, error: err.message };
      }
    })
  );
}
