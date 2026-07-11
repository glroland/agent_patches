import * as fleet from '../services/fleet.js';
import { concerns } from '../services/activity.js';
import { invalidate as invalidateBriefing } from '../services/briefingCache.js';

const SEVERITIES = ['critical', 'warning', 'info'];

// GET /api/issues — aggregated concerns/findings flagged by agents, plus
// per-severity counts for the Issues screen's summary cards.
export async function listIssues(req, res, next) {
  try {
    const items = concerns(await fleet.listFleet());
    const counts = Object.fromEntries(SEVERITIES.map((s) => [s, 0]));
    for (const item of items) {
      counts[item.severity] = (counts[item.severity] ?? 0) + 1;
    }
    res.json({ items, counts });
  } catch (err) {
    next(err);
  }
}

// POST /api/issues/:id/resolve — dismiss a specific finding so it stops
// showing up as an open concern. Body: { agentId: string }
export async function resolveIssue(req, res, next) {
  try {
    const { id } = req.params;
    const { agentId } = req.body ?? {};

    if (!agentId) {
      return res.status(400).json({ message: 'agentId is required' });
    }

    const result = await fleet.resolveFinding(agentId, id);
    invalidateBriefing();
    res.json(result);
  } catch (err) {
    next(err);
  }
}
