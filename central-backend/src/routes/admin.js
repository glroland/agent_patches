import { Router } from 'express';
import * as fleet from '../services/fleet.js';

const router = Router();

// DELETE /api/admin/memory — clear memory on every enrolled agent in parallel.
// Returns an array of per-agent results.
router.delete('/memory', async (req, res, next) => {
  try {
    const results = await fleet.clearAllAgentsMemory();
    res.json({ results });
  } catch (err) {
    next(err);
  }
});

export default router;
