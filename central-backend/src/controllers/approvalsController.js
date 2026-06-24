import * as fleet from '../services/fleet.js';
import { pendingApprovals } from '../services/activity.js';
import { invalidate as invalidateBriefing } from '../services/briefingCache.js';

// GET /api/approvals — pending approval requests across the fleet, sorted by
// risk then age.
export async function listApprovals(req, res, next) {
  try {
    res.json(pendingApprovals(await fleet.listFleet()));
  } catch (err) {
    next(err);
  }
}

// POST /api/approvals/:id/decision — approve or reject a pending request.
// Body: { decision: "approved"|"rejected", agentId: string, reason?: string }
export async function decideApproval(req, res, next) {
  try {
    const { id } = req.params;
    const { decision, agentId, reason = '' } = req.body ?? {};

    if (!agentId) {
      return res.status(400).json({ message: 'agentId is required' });
    }
    if (decision !== 'approved' && decision !== 'rejected') {
      return res.status(400).json({ message: 'decision must be "approved" or "rejected"' });
    }

    const result = await fleet.resolveApproval(agentId, id, decision, reason);
    invalidateBriefing();
    res.json(result);
  } catch (err) {
    next(err);
  }
}
