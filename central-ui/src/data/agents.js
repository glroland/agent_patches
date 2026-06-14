// Helpers over the live agent list (inventory + mock activity merged by
// useAgents). Each endpoint is managed by its own AI agent that polls the
// host, makes observations, takes routine actions on its own, and surfaces
// recommendations or approval requests for a human operator. These helpers
// derive activity feeds, pending approvals, and concerns from that list —
// the manager cares about what each agent is seeing, doing, and asking for,
// not dashboards of percentages.

export { STATUS_META } from './mockActivity';

export function allTimelineEntries(agents) {
  return agents.flatMap((agent) =>
    agent.timeline.map((entry) => ({ ...entry, agentId: agent.id, hostname: agent.hostname }))
  );
}

export function recentActivity(agents, limit = 10) {
  return allTimelineEntries(agents)
    .sort((a, b) => new Date(b.time) - new Date(a.time))
    .slice(0, limit);
}

export function pendingApprovals(agents) {
  const riskOrder = { high: 0, medium: 1, low: 2 };
  return allTimelineEntries(agents)
    .filter((entry) => entry.type === 'approval' && entry.status === 'pending')
    .sort((a, b) => riskOrder[a.risk] - riskOrder[b.risk] || new Date(a.time) - new Date(b.time));
}

export function concerns(agents) {
  const severityOrder = { critical: 0, warning: 1, info: 2 };
  const items = [];

  for (const agent of agents) {
    if (agent.status === 'offline') {
      items.push({
        id: `${agent.id}-offline`,
        severity: 'critical',
        agentId: agent.id,
        hostname: agent.hostname,
        title: 'Agent is offline',
        detail: `Last heard from at ${new Date(agent.lastPoll).toLocaleString()}.`,
        time: agent.lastPoll,
      });
    }
    for (const entry of agent.timeline) {
      if (entry.severity) {
        items.push({
          id: entry.id,
          severity: entry.severity,
          agentId: agent.id,
          hostname: agent.hostname,
          title: entry.title,
          detail: entry.detail,
          time: entry.time,
        });
      }
    }
  }

  return items.sort((a, b) => severityOrder[a.severity] - severityOrder[b.severity] || new Date(b.time) - new Date(a.time));
}
