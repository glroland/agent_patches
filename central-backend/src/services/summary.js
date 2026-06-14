// Fleet-wide counts used by the navigation shell (badge counts, enrolled
// agent total).

import { listFleet } from './fleet.js';
import { pendingApprovals, concerns } from './activity.js';

const ATTENTION_STATUSES = ['attention', 'offline'];

export function getSummary() {
  const agents = listFleet();
  const attentionCount = agents.filter((a) => ATTENTION_STATUSES.includes(a.status)).length;
  const pendingApprovalCount = pendingApprovals(agents).length;
  const criticalIssueCount = concerns(agents).filter((c) => c.severity === 'critical').length;

  return {
    totalAgents: agents.length,
    attentionCount,
    pendingApprovalCount,
    criticalIssueCount,
  };
}
