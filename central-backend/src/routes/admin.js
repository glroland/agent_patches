import { Router } from 'express';
import * as fleet from '../services/fleet.js';
import * as gatewayService from '../services/gatewayService.js';
import * as intelligenceStore from '../services/intelligenceStore.js';
import * as intelligenceCache from '../services/intelligenceCache.js';
import * as briefingCache from '../services/briefingCache.js';

const router = Router();

// DELETE /api/admin/memory — purges agent memory across every enrolled agent,
// resets the llm-gateway's in-memory stats and persisted data file back to
// zero, and clears central's own agent-derived caches (fleet intelligence
// reports and the operator briefing). Chat history is left untouched — it's
// user data, not agent memory.
router.delete('/memory', async (req, res, next) => {
  try {
    const agents = await fleet.clearAllAgentsMemory();

    let gateway;
    try {
      const result = await gatewayService.resetStats();
      gateway = { ok: true, skipped: result === null };
    } catch (err) {
      gateway = { ok: false, error: err.message };
    }

    intelligenceStore.clearAll();
    intelligenceCache.clear();
    briefingCache.clear();

    // Re-poll immediately so the fleet cache reflects the cleared state;
    // otherwise the dashboard keeps showing stale timeline data until the
    // next scheduled poll cycle.
    await fleet.refreshFleet();

    res.json({ agents, gateway });
  } catch (err) {
    next(err);
  }
});

export default router;
