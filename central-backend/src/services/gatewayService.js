import { config } from '../config/index.js';

export async function getStats() {
  if (!config.gateway.statsUrl) {
    return null;
  }
  const resp = await fetch(`${config.gateway.statsUrl}/stats`, {
    signal: AbortSignal.timeout(5000),
  });
  if (!resp.ok) {
    throw new Error(`gateway stats responded ${resp.status}`);
  }
  return resp.json();
}
