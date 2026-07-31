import { WebSocketServer } from 'ws';
import { logger } from '../utils/logger.js';
import { validateWsToken } from '../middleware/auth.js';
import { getFleet, subscribe as subscribeFleet } from './fleetCache.js';
import { getReport, getStatus as getIntelligenceStatus, subscribe as subscribeIntelligence, subscribeStatus as subscribeIntelligenceStatus } from './intelligenceCache.js';
import { getBriefing, subscribe as subscribeBriefing } from './briefingCache.js';
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
  const attentionAgents = rawAgents.filter((a) =>
    ATTENTION_STATUSES.includes(a.status) ||
    (a.timeline ?? []).some((e) => e.severity === 'critical'));
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
      lastPatchedAt: agent.lastPatchedAt ?? null,
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
      hasHighImportanceApproval: allApprovals.some((a) => a.importance === 'high'),
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
  const intelligenceStatus = getIntelligenceStatus();
  const briefing = getBriefing();

  return JSON.stringify({ type: 'fleet_update', agents, dashboard, summary, intelligence, intelligenceStatus, briefing });
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

  wss.on('connection', async (ws, req) => {
    const url   = new URL(req.url, 'http://localhost');
    const token = url.searchParams.get('token');

    if (!(await validateWsToken(token))) {
      ws.close(1008, 'Unauthorized');
      return;
    }

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

  // Also broadcast when a new intelligence report or briefing arrives.
  subscribeIntelligence(() => {
    const fleet = getFleet();
    if (!fleet) {
      logger.warn('ws: intelligence report arrived but fleet cache is empty — skipping broadcast');
      return;
    }
    logger.info(`ws: broadcasting new intelligence report to ${wss?.clients.size ?? 0} client(s)`);
    broadcast(buildPayload(fleet));
  });

  subscribeBriefing(() => {
    const fleet = getFleet();
    if (fleet) broadcast(buildPayload(fleet));
  });

  // Also broadcast the moment an analysis run starts/finishes, so the
  // "Analysing…" state and any failure reason reach clients (including ones
  // that connect or refresh mid-run) without waiting for a full report.
  subscribeIntelligenceStatus(() => {
    const fleet = getFleet();
    if (fleet) broadcast(buildPayload(fleet));
  });

  logger.info('ws: hub attached at /ws');
}
