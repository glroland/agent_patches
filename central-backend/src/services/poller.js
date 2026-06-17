import { config } from '../config/index.js';
import { logger } from '../utils/logger.js';
import { fetchAllAgents } from './fleet.js';
import { setFleet } from './fleetCache.js';

let timer = null;

async function pollAllAgents() {
  try {
    const agents = await fetchAllAgents();
    setFleet(agents);
    logger.info(`poller: updated ${agents.length} agent(s)`);
  } catch (err) {
    logger.error(`poller: poll failed: ${err.message}`);
  }
}

export function start() {
  if (timer) return;
  const intervalMs = config.agents.pollIntervalSeconds * 1000;
  logger.info(`poller: starting, interval ${config.agents.pollIntervalSeconds}s`);
  pollAllAgents();
  timer = setInterval(pollAllAgents, intervalMs);
}

export function stop() {
  if (!timer) return;
  clearInterval(timer);
  timer = null;
}
