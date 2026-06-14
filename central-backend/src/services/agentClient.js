// Client for talking to an individual endpoint-server's HTTP API.

const DEFAULT_TIMEOUT_MS = 3000;

export class AgentClient {
  constructor({ fqdn, port, timeoutMs = DEFAULT_TIMEOUT_MS } = {}) {
    this.baseUrl = `http://${fqdn}:${port}`;
    this.timeoutMs = timeoutMs;
  }

  // Fetches GET /status. Returns null if the agent is unreachable, responds
  // with a non-2xx status, or returns invalid JSON — callers should treat
  // null as "offline".
  async getStatus() {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.timeoutMs);
    try {
      const res = await fetch(`${this.baseUrl}/status`, { signal: controller.signal });
      if (!res.ok) return null;
      return await res.json();
    } catch {
      return null;
    } finally {
      clearTimeout(timer);
    }
  }
}
