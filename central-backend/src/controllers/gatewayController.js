import * as gatewayService from '../services/gatewayService.js';

export async function getStats(req, res, next) {
  try {
    const stats = await gatewayService.getStats();
    if (stats === null) {
      return res.status(503).json({ error: 'Gateway stats URL not configured (set GATEWAY_STATS_URL)' });
    }
    res.json(stats);
  } catch (err) {
    next(err);
  }
}

export async function getPending(req, res, next) {
  try {
    const data = await gatewayService.getPending();
    if (data === null) {
      return res.status(503).json({ error: 'Gateway stats URL not configured (set GATEWAY_STATS_URL)' });
    }
    res.json(data);
  } catch (err) {
    next(err);
  }
}
