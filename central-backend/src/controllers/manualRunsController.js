import * as fleet from '../services/fleet.js';
import { logger } from '../utils/logger.js';

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

    const username = req.user?.username ?? 'unknown';
    const result = await fleet.submitManualRunResult(agentId, id, output, status);
    logger.info(`manualRunsController.submitResult: ${username} submitted "${status}" for manual run ${id} on ${agentId} (${output.length} char output)`);
    res.json(result);
  } catch (err) {
    logger.error(`manualRunsController.submitResult: failed for manual run ${req.params.id} on ${req.body?.agentId}: ${err.message}`);
    next(err);
  }
}
