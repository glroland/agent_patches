import { Router } from 'express';
import { logger } from '../utils/logger.js';
import { getReport } from '../services/intelligenceCache.js';
import { refresh } from '../services/intelligence.js';

const router = Router();

// GET /api/intelligence — latest fleet intelligence report.
// Returns 204 if no report has been generated yet.
router.get('/', (req, res) => {
  const report = getReport();
  if (!report) return res.status(204).end();
  res.json(report);
});

// POST /api/intelligence/refresh — trigger an immediate fleet analysis
// instead of waiting for the next scheduled interval. The run happens in the
// background; the new report is broadcast to UI clients via the WS hub.
// Returns 202 when a run is started (or already underway), 503 if disabled.
router.post('/refresh', (req, res) => {
  logger.info('intelligence: POST /refresh received');
  const result = refresh();
  if (!result.started) {
    logger.warn('intelligence: refresh rejected — fleet intelligence not configured (INTELLIGENCE_BASE_URL unset)');
    return res.status(503).json({ message: 'Fleet intelligence is not configured.' });
  }
  logger.info(`intelligence: refresh ${result.alreadyRunning ? 'already running, joining in-flight run' : 'started a new background run'}`);
  res.status(202).json(result);
});

export default router;
