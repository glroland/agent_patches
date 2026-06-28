import * as fleet from '../services/fleet.js';

// POST /api/manual-runs/:id/result — submit output for a pending manual-run request.
// Body: { output: string, status: "completed"|"skipped", agentId: string }
export async function submitResult(req, res, next) {
  try {
    const { id } = req.params;
    const { output = '', status, agentId } = req.body ?? {};

    if (!agentId) {
      return res.status(400).json({ message: 'agentId is required' });
    }
    if (status !== 'completed' && status !== 'skipped') {
      return res.status(400).json({ message: 'status must be "completed" or "skipped"' });
    }

    const result = await fleet.submitManualRunResult(agentId, id, output, status);
    res.json(result);
  } catch (err) {
    next(err);
  }
}
