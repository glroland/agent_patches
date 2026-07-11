// Aggregates fleet data into the single payload the Dashboard screen needs:
// headline stats, agents needing attention, top pending approvals, and the
// most recent activity across the fleet.

import { listFleet } from './fleet.js';
import { recentActivity, pendingApprovals } from './activity.js';

const ATTENTION_STATUSES = ['attention', 'offline'];
const ACTIVITY_LIMIT = 8;
const APPROVAL_LIMIT = 4;

export async function getDashboard() {
  const agents = await listFleet();
  const attention = agents.filter((a) => ATTENTION_STATUSES.includes(a.status));
  const approvals = pendingApprovals(agents);
  // Approvals have their own dedicated page — exclude them from the activity feed.
  const activity = recentActivity(agents, ACTIVITY_LIMIT).filter((e) => e.type !== 'approval');
  const openRecommendations = agents.flatMap((a) => a.timeline.filter((t) => t.type === 'recommendation')).length;

  return {
    stats: {
      totalAgents: agents.length,
      healthyAgents: agents.length - attention.length,
      attentionCount: attention.length,
      pendingApprovalCount: approvals.length,
      openRecommendations,
      hasHighRiskApproval: approvals.some((a) => a.risk === 'high'),
      hasHighImportanceApproval: approvals.some((a) => a.importance === 'high'),
    },
    attention: attention.map((a) => ({
      id: a.id,
      hostname: a.hostname,
      status: a.status,
      statusLabel: a.statusLabel,
      currentTask: a.currentTask,
      lastPoll: a.lastPoll,
    })),
    approvals: approvals.slice(0, APPROVAL_LIMIT),
    activity,
  };
}
