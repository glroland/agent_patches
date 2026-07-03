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
