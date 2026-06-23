// Fleet-wide counts used by the navigation shell (badge counts, enrolled
// agent total).

import { listFleet } from './fleet.js';
import { pendingApprovals, concerns } from './activity.js';

const ATTENTION_STATUSES = ['attention', 'offline'];

export async function getSummary() {
  const agents = await listFleet();
  const attentionCount = agents.filter((a) =>
    ATTENTION_STATUSES.includes(a.status) ||
    (a.timeline ?? []).some((e) => e.severity === 'critical')).length;
  const pending = pendingApprovals(agents);
  const pendingApprovalCount = pending.length;
  const criticalIssueCount = concerns(agents).filter((c) => c.severity === 'critical').length;

  // Oldest pending approval time lets the UI colour the nav badge by age.
  const oldestPendingApprovalTime = pending.length > 0
    ? pending.reduce((oldest, a) => (a.time < oldest ? a.time : oldest), pending[0].time)
    : null;

  return {
    totalAgents: agents.length,
    attentionCount,
    pendingApprovalCount,
    oldestPendingApprovalTime,
    criticalIssueCount,
  };
}
