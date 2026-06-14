import { notImplemented } from '../utils/notImplemented.js';
import * as fleet from '../services/fleet.js';
import { pendingApprovals } from '../services/activity.js';

// GET /api/approvals — pending approval requests across the fleet, sorted by
// risk then age.
export async function listApprovals(req, res, next) {
  try {
    res.json(pendingApprovals(await fleet.listFleet()));
  } catch (err) {
    next(err);
  }
}

// POST /api/approvals/:id/decision — approve or reject a pending request
export const decideApproval = notImplemented;
