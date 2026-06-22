import { Router } from 'express';
import * as fleet from '../services/fleet.js';

const router = Router();

// DELETE /api/admin/memory — clear memory on every enrolled agent in parallel.
// Returns an array of per-agent results.
router.delete('/memory', async (req, res, next) => {
  try {
    const results = await fleet.clearAllAgentsMemory();
    // Re-poll immediately so the fleet cache reflects the cleared state;
    // otherwise the dashboard keeps showing stale timeline data until the
    // next scheduled poll cycle.
    await fleet.refreshFleet();
    res.json({ results });
  } catch (err) {
    next(err);
  }
});

export default router;
