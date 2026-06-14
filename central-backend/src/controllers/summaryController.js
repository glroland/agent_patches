import * as summaryService from '../services/summary.js';

// GET /api/summary — fleet-wide counts for the navigation shell (enrolled
// agent total and badge counts for agents/approvals/issues).
export function getSummary(req, res, next) {
  try {
    res.json(summaryService.getSummary());
  } catch (err) {
    next(err);
  }
}
