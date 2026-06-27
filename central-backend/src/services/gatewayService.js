import { config } from '../config/index.js';

export async function getStats() {
  if (!config.gateway.statsUrl) {
    return null;
  }
  const headers = {};
  if (config.gateway.authToken) {
    headers['Authorization'] = `Bearer ${config.gateway.authToken}`;
  }
  const resp = await fetch(`${config.gateway.statsUrl}/stats`, {
    headers,
    signal: AbortSignal.timeout(5000),
  });
  if (!resp.ok) {
    throw new Error(`gateway stats responded ${resp.status}`);
  }
  return resp.json();
}
