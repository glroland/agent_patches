// Client for talking to an individual endpoint-server's HTTP API.

const DEFAULT_TIMEOUT_MS = 3000;

// Sending a chat message runs the agent's full tool-use loop, which can take
// much longer than a /status poll.
const DEFAULT_MESSAGE_TIMEOUT_MS = 60000;

export class AgentClient {
  constructor({ fqdn, port, timeoutMs = DEFAULT_TIMEOUT_MS, authToken } = {}) {
    this.baseUrl = `http://${fqdn}:${port}`;
    this.timeoutMs = timeoutMs;
    this.authToken = authToken;
  }

  // Fetches GET /status. Returns null if the agent is unreachable, responds
  // with a non-2xx status, or returns invalid JSON — callers should treat
  // null as "offline".
  async getStatus() {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.timeoutMs);
    try {
      const headers = {};
      if (this.authToken) {
        headers.Authorization = `Bearer ${this.authToken}`;
      }

      const res = await fetch(`${this.baseUrl}/status`, { headers, signal: controller.signal });
      if (!res.ok) return null;
      return await res.json();
    } catch {
      return null;
    } finally {
      clearTimeout(timer);
    }
  }

  // Fetches GET /memory. Returns null if the agent is unreachable, responds
  // with a non-2xx status, or returns invalid JSON.
  async getMemory() {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.timeoutMs);
    try {
      const headers = {};
      if (this.authToken) {
        headers.Authorization = `Bearer ${this.authToken}`;
      }

      const res = await fetch(`${this.baseUrl}/memory`, { headers, signal: controller.signal });
      if (!res.ok) return null;
      return await res.json();
    } catch {
      return null;
    } finally {
      clearTimeout(timer);
    }
  }

  // Sends a chat message to the agent via the A2A JSON-RPC "message/send"
  // method and returns its text reply. Throws on a network error, timeout,
  // non-2xx response, or a JSON-RPC error response.
  async sendMessage(text, { timeoutMs = DEFAULT_MESSAGE_TIMEOUT_MS } = {}) {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs);
    try {
      const headers = { 'Content-Type': 'application/json' };
      if (this.authToken) {
        headers.Authorization = `Bearer ${this.authToken}`;
      }

      const res = await fetch(this.baseUrl, {
        method: 'POST',
        headers,
        signal: controller.signal,
        body: JSON.stringify({
          jsonrpc: '2.0',
          id: crypto.randomUUID(),
          method: 'SendMessage',
          params: {
            message: {
              kind: 'message',
              messageId: crypto.randomUUID(),
              role: 'user',
              parts: [{ kind: 'text', text }],
            },
          },
        }),
      });

      if (!res.ok) {
        throw new Error(`agent returned ${res.status} ${res.statusText}`);
      }

      const body = await res.json();
      if (body.error) {
        throw new Error(`agent error: ${body.error.message ?? JSON.stringify(body.error)}`);
      }

      return extractText(body.result);
    } finally {
      clearTimeout(timer);
    }
  }
}

// Pulls all text parts out of a JSON-RPC SendMessage result, which wraps
// either a Message ({ message: {...} }) or a Task ({ task: {...} }).
function extractText(result) {
  if (!result) return '';

  if (result.message) {
    return (result.message.parts ?? []).map((p) => p.text ?? '').join('');
  }

  if (result.task) {
    const fromStatus = (result.task.status?.message?.parts ?? []).map((p) => p.text ?? '').join('');
    const fromArtifacts = (result.task.artifacts ?? [])
      .flatMap((a) => a.parts ?? [])
      .map((p) => p.text ?? '')
      .join('');
    return fromStatus + fromArtifacts;
  }

  return '';
}
