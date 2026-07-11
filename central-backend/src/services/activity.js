// Derives activity feeds, pending approvals, and concerns from a list of
// fleet agents (see ./fleet.js). Each endpoint's agent reports observations,
// actions, recommendations, and approval requests on its timeline; these
// helpers flatten and sort that activity for the screens that need it.

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
    .filter((entry) => (entry.type === 'approval' || entry.type === 'manual_run') && entry.status === 'pending')
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
        detail: agent.lastPoll
          ? `Last heard from at ${new Date(agent.lastPoll).toLocaleString()}.`
          : 'Not responding to status polls.',
        time: agent.lastPoll,
      });
    }
    for (const entry of agent.timeline) {
      if (entry.severity && entry.status !== 'resolved') {
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
