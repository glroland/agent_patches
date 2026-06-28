// Merges the read-only CSV inventory with each agent's live GET /status
// response into the full fleet view consumed by the other services/controllers.

import * as inventory from './inventory.js';
import { AgentClient } from './agentClient.js';
import { config } from '../config/index.js';
import { getFleet, setFleet } from './fleetCache.js';

const STATUS_META = {
  active: { label: 'Active', description: 'Currently working on a task' },
  idle:   { label: 'Idle',   description: 'Healthy, nothing pending' },
  offline:{ label: 'Offline',description: 'Not responding to polls' },
};

function shortHost(fqdn) {
  return fqdn.split('.')[0].toLowerCase();
}

// Builds a human-readable attention summary from the timeline so the operator
// knows exactly what needs review without opening the agent detail page.
function attentionDescription(timeline) {
  const parts = [];

  const escalations = timeline.filter((e) => e.type === 'escalation');
  if (escalations.length > 0) {
    const first = escalations[0];
    // Strip the "ESCALATION: " prefix the skill prepends for readability.
    const title = first.title.replace(/^ESCALATION:\s*/i, '');
    parts.push(`Escalation: ${title}`);
  }

  const criticals = timeline.filter(
    (e) => e.severity === 'critical' && e.type !== 'escalation'
  );
  if (criticals.length > 0) {
    const label = criticals.length === 1
      ? criticals[0].title
      : `${criticals.length} critical issues`;
    parts.push(label);
  }

  const pendingApprovals = timeline.filter(
    (e) => e.type === 'approval' && e.status === 'pending' &&
           (e.risk === 'high' || e.risk === 'medium')
  );
  if (pendingApprovals.length > 0) {
    const highCount  = pendingApprovals.filter((e) => e.risk === 'high').length;
    const medCount   = pendingApprovals.filter((e) => e.risk === 'medium').length;
    const riskLabels = [
      highCount  ? `${highCount} high-risk`   : null,
      medCount   ? `${medCount} medium-risk`  : null,
    ].filter(Boolean).join(', ');
    const noun = pendingApprovals.length === 1 ? 'approval' : 'approvals';
    parts.push(`${riskLabels} ${noun} waiting`);
  }

  return parts.length > 0 ? parts.join(' · ') : 'Needs review';
}

async function toFleetAgent(inventoryAgent) {
  const id = shortHost(inventoryAgent.fqdn);
  const client = new AgentClient({
    fqdn: inventoryAgent.fqdn,
    port: inventoryAgent.port,
    authToken: config.agents.authToken,
    timeoutMs: config.agents.pollTimeoutMs,
  });
  const data = await client.getStatus();

  const agentInfo = data?.agent ?? {};
  const statusBlock = data?.status ?? { state: 'offline', lastPoll: null, currentTask: null };
  const timeline = data?.timeline ?? [];
  const state = data ? statusBlock.state : 'offline';
  const statusMeta = STATUS_META[state] ?? STATUS_META.offline;
  const statusDescription = state === 'attention'
    ? (data?.statusDescription || attentionDescription(timeline))
    : statusMeta.description;

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
    statusDescription,
    lastPoll: statusBlock.lastPoll ?? null,
    currentTask: statusBlock.currentTask ?? null,
    lastPatchedAt: data?.lastPatchedAt ?? null,
    buildTime: agentInfo.buildTime ?? null,
    timeline,
    diskTrends: data?.diskTrends ?? null,
    smartTrends: data?.smartTrends ?? null,
  };
}

// Polls every enrolled agent in parallel. Called by the poller; also used as
// a fallback by listFleet() before the first poll cycle completes.
export async function fetchAllAgents() {
  return Promise.all(inventory.listAgents().map(toFleetAgent));
}

// Polls all agents immediately and updates the fleet cache. Used to force a
// cache refresh after a destructive operation (e.g. clearing agent memory).
export async function refreshFleet() {
  const agents = await fetchAllAgents();
  setFleet(agents);
  return agents;
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
    timeoutMs: config.agents.pollTimeoutMs,
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
    timeoutMs: config.agents.pollTimeoutMs,
  });
  return client.sendMessage(text, { timeoutMs: config.agents.messageTimeoutMs });
}

export async function submitManualRunResult(agentId, manualRunId, output, status) {
  const inventoryAgent = inventory.listAgents().find((agent) => shortHost(agent.fqdn) === agentId);
  if (!inventoryAgent) {
    throw new Error(`agent ${agentId} not found in inventory`);
  }
  const client = new AgentClient({
    fqdn: inventoryAgent.fqdn,
    port: inventoryAgent.port,
    authToken: config.agents.authToken,
    timeoutMs: config.agents.pollTimeoutMs,
  });
  return client.submitManualRunResult(manualRunId, output, status);
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
    timeoutMs: config.agents.pollTimeoutMs,
  });
  return client.resolveApproval(approvalId, decision, reason);
}

// Fetches GET /log from a single agent. Returns undefined if not in inventory,
// null if unreachable, or the log payload on success.
export async function getAgentLog(id) {
  const inventoryAgent = inventory.listAgents().find((agent) => shortHost(agent.fqdn) === id);
  if (!inventoryAgent) return undefined;
  const client = new AgentClient({
    fqdn: inventoryAgent.fqdn,
    port: inventoryAgent.port,
    authToken: config.agents.authToken,
    timeoutMs: config.agents.pollTimeoutMs,
  });
  return client.getLog();
}

// Fetches GET /responsibilities from a single agent. Returns undefined if not
// in inventory, null if unreachable, or the array of responsibility items.
export async function getAgentResponsibilities(id) {
  const inventoryAgent = inventory.listAgents().find((agent) => shortHost(agent.fqdn) === id);
  if (!inventoryAgent) return undefined;
  const client = new AgentClient({
    fqdn: inventoryAgent.fqdn,
    port: inventoryAgent.port,
    authToken: config.agents.authToken,
    timeoutMs: config.agents.pollTimeoutMs,
  });
  return client.getResponsibilities();
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
    timeoutMs: config.agents.pollTimeoutMs,
  });
  return client.clearMemory();
}

// Fetches /.well-known/agent-card.json from a single agent. Returns undefined
// if not in inventory, null if unreachable, or the card payload on success.
export async function getAgentCard(id) {
  const inventoryAgent = inventory.listAgents().find((agent) => shortHost(agent.fqdn) === id);
  if (!inventoryAgent) return undefined;
  const client = new AgentClient({
    fqdn: inventoryAgent.fqdn,
    port: inventoryAgent.port,
    authToken: config.agents.authToken,
    timeoutMs: config.agents.pollTimeoutMs,
  });
  return client.getAgentCard();
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
        timeoutMs: config.agents.pollTimeoutMs,
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
        timeoutMs: config.agents.pollTimeoutMs,
      });
      try {
        const reply = await client.sendMessage(text, { timeoutMs: config.agents.messageTimeoutMs });
        return { id, hostname: inventoryAgent.fqdn, displayName: inventoryAgent.displayName, reply };
      } catch (err) {
        return { id, hostname: inventoryAgent.fqdn, displayName: inventoryAgent.displayName, error: err.message };
      }
    })
  );
}
