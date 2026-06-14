import * as dashboardService from '../services/dashboard.js';

// GET /api/dashboard — everything the Dashboard screen needs in one call:
// headline stats, agents needing attention, top pending approvals, and
// recent fleet activity.
export function getDashboard(req, res, next) {
  try {
    res.json(dashboardService.getDashboard());
  } catch (err) {
    next(err);
  }
}
