import { config } from '../config/index.js';

function gatewayHeaders() {
  const headers = {};
  if (config.gateway.authToken) {
    headers['Authorization'] = `Bearer ${config.gateway.authToken}`;
  }
  return headers;
}

export async function getStats() {
  if (!config.gateway.statsUrl) {
    return null;
  }
  const resp = await fetch(`${config.gateway.statsUrl}/stats`, {
    headers: gatewayHeaders(),
    signal: AbortSignal.timeout(5000),
  });
  if (!resp.ok) {
    throw new Error(`gateway stats responded ${resp.status}`);
  }
  return resp.json();
}

export async function getPending() {
  if (!config.gateway.statsUrl) {
    return null;
  }
  const resp = await fetch(`${config.gateway.statsUrl}/pending`, {
    headers: gatewayHeaders(),
    signal: AbortSignal.timeout(5000),
  });
  if (!resp.ok) {
    throw new Error(`gateway pending responded ${resp.status}`);
  }
  return resp.json();
}

// Polls the gateway's own readiness check (GET /health/ready), which
// reflects whether the gateway can reach its configured upstream LLM and
// see the configured model in its OpenAI-compatible /v1/models list.
// Returns null when the gateway isn't configured (stats URL unset), so
// central-backend's own readiness doesn't block on a dependency that was
// never wired up (e.g. local dev).
export async function getHealth() {
  if (!config.gateway.statsUrl) {
    return null;
  }
  const resp = await fetch(`${config.gateway.statsUrl}/health/ready`, {
    headers: gatewayHeaders(),
    signal: AbortSignal.timeout(5000),
  });
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
  const resp = await fetch(`${config.gateway.statsUrl}/stats`, {
    method: 'DELETE',
    headers: gatewayHeaders(),
    signal: AbortSignal.timeout(5000),
  });
  if (!resp.ok) {
    throw new Error(`gateway reset responded ${resp.status}`);
  }
  return resp.json();
}
