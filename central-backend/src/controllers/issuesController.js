import * as fleet from '../services/fleet.js';
import { concerns } from '../services/activity.js';

const SEVERITIES = ['critical', 'warning', 'info'];

// GET /api/issues — aggregated concerns/findings flagged by agents, plus
// per-severity counts for the Issues screen's summary cards.
export function listIssues(req, res, next) {
  try {
    const items = concerns(fleet.listFleet());
    const counts = Object.fromEntries(SEVERITIES.map((s) => [s, 0]));
    for (const item of items) {
      counts[item.severity] = (counts[item.severity] ?? 0) + 1;
    }
    res.json({ items, counts });
  } catch (err) {
    next(err);
  }
}
