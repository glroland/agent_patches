import { config } from '../config/index.js';
import { logger } from '../utils/logger.js';

function gatewayHeaders() {
  const headers = {};
  if (config.gateway.authToken) {
    headers['Authorization'] = `Bearer ${config.gateway.authToken}`;
  }
  return headers;
}

// Wraps a gateway fetch so a network error or timeout is logged with the
// target URL before it propagates — without this, callers only ever see a
// generic "fetch failed"/"aborted" message with no indication which gateway
// endpoint or host was involved.
async function gatewayFetch(path, options) {
  const url = `${config.gateway.statsUrl}${path}`;
  try {
    return await fetch(url, { headers: gatewayHeaders(), signal: AbortSignal.timeout(5000), ...options });
  } catch (err) {
    const reason = err.name === 'TimeoutError' ? 'timed out after 5000ms' : err.message;
    logger.warn(`gatewayService: request to ${url} failed — ${reason}`);
    throw err;
  }
}

export async function getStats() {
  if (!config.gateway.statsUrl) {
    return null;
  }
  const resp = await gatewayFetch('/stats');
  if (!resp.ok) {
    logger.warn(`gatewayService: GET /stats responded ${resp.status}`);
    throw new Error(`gateway stats responded ${resp.status}`);
  }
  return resp.json();
}

export async function getPending() {
  if (!config.gateway.statsUrl) {
    return null;
  }
  const resp = await gatewayFetch('/pending');
  if (!resp.ok) {
    logger.warn(`gatewayService: GET /pending responded ${resp.status}`);
    throw new Error(`gateway pending responded ${resp.status}`);
  }
  return resp.json();
}

export async function getHistory() {
  if (!config.gateway.statsUrl) {
    return null;
  }
  const resp = await gatewayFetch('/stats/history');
  if (!resp.ok) {
    logger.warn(`gatewayService: GET /stats/history responded ${resp.status}`);
    throw new Error(`gateway history responded ${resp.status}`);
  }
  return resp.json();
}

// Polls the gateway's plain liveness check (GET /health) — confirms the
// gateway process itself is up and responding. Deliberately does NOT use
// GET /health/ready, which makes the gateway perform an upstream
// GET /v1/models call: that check can queue/contend behind real inference
// traffic on single-request upstream LLM servers, so it must never sit in
// central-backend's own readiness path (a slow/busy LLM would otherwise
// cascade into central-backend, and then central-ui, going NotReady).
// Returns null when the gateway isn't configured (stats URL unset), so
// central-backend's own readiness doesn't block on a dependency that was
// never wired up (e.g. local dev).
export async function getHealth() {
  if (!config.gateway.statsUrl) {
    return null;
  }
  const resp = await gatewayFetch('/health');
  let body;
  try {
    body = await resp.json();
  } catch {
    body = null;
  }
  return { ok: resp.ok, status: resp.status, body };
}

// Resets the gateway's in-memory token/request stats to zero and flushes that
// empty state to its persisted data file. Returns null when the gateway
// isn't configured (stats URL unset) so callers can treat that as a no-op.
export async function resetStats() {
  if (!config.gateway.statsUrl) {
    return null;
  }
  const resp = await gatewayFetch('/stats', { method: 'DELETE' });
  if (!resp.ok) {
    logger.warn(`gatewayService: DELETE /stats responded ${resp.status}`);
    throw new Error(`gateway reset responded ${resp.status}`);
  }
  logger.info('gatewayService: stats reset');
  return resp.json();
}
