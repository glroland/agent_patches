import { WebSocketServer } from 'ws';
import { logger } from '../utils/logger.js';
import { getFleet, subscribe as subscribeFleet } from './fleetCache.js';
import { getReport, subscribe as subscribeIntelligence } from './intelligenceCache.js';
import { pendingApprovals, recentActivity, concerns } from './activity.js';

const PING_INTERVAL_MS = 30000;
const ACTIVITY_LIMIT = 8;
const APPROVAL_LIMIT = 4;
const ATTENTION_STATUSES = ['attention', 'offline'];

let wss = null;

// Builds the single broadcast payload from the raw fleet agents array,
// producing the same shapes the HTTP controllers return so UI pages need
// no transformation logic.
function buildPayload(rawAgents) {
  const allApprovals = pendingApprovals(rawAgents);
  const attentionAgents = rawAgents.filter((a) => ATTENTION_STATUSES.includes(a.status));
  const openRecommendations = rawAgents
    .flatMap((a) => a.timeline.filter((t) => t.type === 'recommendation')).length;

  const agents = rawAgents.map((agent) => {
    const latest = agent.timeline[0];
    return {
      id: agent.id,
      hostname: agent.hostname,
      displayName: agent.displayName,
      role: agent.role,
      os: agent.os,
      tags: agent.tags,
      status: agent.status,
      statusLabel: agent.statusLabel,
      statusDescription: agent.statusDescription,
      currentTask: agent.currentTask,
      lastPoll: agent.lastPoll,
      latestActivity: latest ? { title: latest.title, time: latest.time, type: latest.type } : null,
      pendingApprovalCount: pendingApprovals([agent]).length,
    };
  });

  const dashboard = {
    stats: {
      totalAgents: rawAgents.length,
      healthyAgents: rawAgents.length - attentionAgents.length,
      attentionCount: attentionAgents.length,
      pendingApprovalCount: allApprovals.length,
      openRecommendations,
      hasHighRiskApproval: allApprovals.some((a) => a.risk === 'high'),
    },
    attention: attentionAgents.map((a) => ({
      id: a.id,
      hostname: a.hostname,
      status: a.status,
      statusLabel: a.statusLabel,
      currentTask: a.currentTask,
      lastPoll: a.lastPoll,
    })),
    approvals: allApprovals.slice(0, APPROVAL_LIMIT),
    activity: recentActivity(rawAgents, ACTIVITY_LIMIT).filter((e) => e.type !== 'approval'),
  };

  const oldestPendingApprovalTime = allApprovals.length > 0
    ? allApprovals.reduce((oldest, a) => (a.time < oldest ? a.time : oldest), allApprovals[0].time)
    : null;

  const summary = {
    totalAgents: rawAgents.length,
    attentionCount: attentionAgents.length,
    pendingApprovalCount: allApprovals.length,
    oldestPendingApprovalTime,
    criticalIssueCount: concerns(rawAgents).filter((c) => c.severity === 'critical').length,
  };

  const intelligence = getReport();

  return JSON.stringify({ type: 'fleet_update', agents, dashboard, summary, intelligence });
}

function broadcast(payload) {
  if (!wss) return;
  for (const client of wss.clients) {
    if (client.readyState === 1 /* OPEN */) {
      client.send(payload);
    }
  }
}

export function attach(server) {
  wss = new WebSocketServer({ server, path: '/ws' });

  wss.on('connection', (ws) => {
    logger.info('ws: client connected');

    const fleet = getFleet();
    if (fleet) {
      ws.send(buildPayload(fleet));
    }

    const pingTimer = setInterval(() => {
      if (ws.readyState === 1) ws.ping();
    }, PING_INTERVAL_MS);

    ws.on('close', () => {
      clearInterval(pingTimer);
      logger.info('ws: client disconnected');
    });

    ws.on('error', (err) => {
      logger.error(`ws: client error: ${err.message}`);
    });
  });

  // Broadcast every time the poller updates the fleet cache.
  subscribeFleet((agents) => broadcast(buildPayload(agents)));

  // Also broadcast when a new intelligence report arrives (may come independently).
  subscribeIntelligence(() => {
    const fleet = getFleet();
    if (fleet) broadcast(buildPayload(fleet));
  });

  logger.info('ws: hub attached at /ws');
}
