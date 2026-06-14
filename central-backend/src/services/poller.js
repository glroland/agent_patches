// Background poller that will periodically query each enrolled agent via
// AgentClient and update the agentRegistry / activity store. Not yet wired
// up — start() is a no-op placeholder so the server can reference it without
// affecting current behavior.

import { config } from '../config/index.js';
import { logger } from '../utils/logger.js';

let timer = null;

export function start() {
  if (timer) return;
  logger.info(`poller: scaffolded, would poll every ${config.agents.pollIntervalSeconds}s (not yet implemented)`);
  // TODO: timer = setInterval(pollAllAgents, config.agents.pollIntervalSeconds * 1000);
}

export function stop() {
  if (!timer) return;
  clearInterval(timer);
  timer = null;
}
