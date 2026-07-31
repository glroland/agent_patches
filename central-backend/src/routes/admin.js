import { Router } from 'express';
import { logger } from '../utils/logger.js';
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
  const username = req.user?.username ?? 'unknown';
  logger.warn(`admin: ${username} triggered a fleet-wide memory purge (DELETE /api/admin/memory)`);
  try {
    const agents = await fleet.clearAllAgentsMemory();
    const failed = agents.filter((a) => !a.ok);
    if (failed.length > 0) {
      logger.warn(`admin: memory purge — ${failed.length}/${agents.length} agent(s) unreachable: ${failed.map((a) => a.hostname).join(', ')}`);
    }

    let gateway;
    try {
      const result = await gatewayService.resetStats();
      gateway = { ok: true, skipped: result === null };
    } catch (err) {
      logger.error(`admin: memory purge — gateway stats reset failed: ${err.message}`);
      gateway = { ok: false, error: err.message };
    }

    intelligenceStore.clearAll();
    intelligenceCache.clear();
    briefingCache.clear();

    // Re-poll immediately so the fleet cache reflects the cleared state;
    // otherwise the dashboard keeps showing stale timeline data until the
    // next scheduled poll cycle.
    await fleet.refreshFleet();

    logger.warn(`admin: memory purge complete by ${username} — ${agents.length - failed.length}/${agents.length} agent(s) cleared, gateway reset: ${gateway.ok ? 'ok' : 'failed'}`);
    res.json({ agents, gateway });
  } catch (err) {
    logger.error(`admin: memory purge by ${username} failed: ${err.message}`);
    next(err);
  }
});

export default router;
