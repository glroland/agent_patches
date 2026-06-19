import { Router } from 'express';
import { getReport } from '../services/intelligenceCache.js';

const router = Router();

// GET /api/intelligence — latest fleet intelligence report.
// Returns 204 if no report has been generated yet.
router.get('/', (req, res) => {
  const report = getReport();
  if (!report) return res.status(204).end();
  res.json(report);
});

export default router;
