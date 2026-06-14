import { test, describe, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';

import { AgentClient } from './agentClient.js';

describe('AgentClient.getStatus', () => {
  let originalFetch;

  beforeEach(() => {
    originalFetch = globalThis.fetch;
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  test('returns parsed JSON on a 200 response', async () => {
    const payload = { agent: { hostname: 'web01' }, status: { state: 'idle' }, timeline: [] };
    globalThis.fetch = async (url) => {
      assert.equal(url, 'http://web01.prod.internal:8080/status');
      return { ok: true, json: async () => payload };
    };

    const client = new AgentClient({ fqdn: 'web01.prod.internal', port: 8080 });
    const result = await client.getStatus();
    assert.deepEqual(result, payload);
  });

  test('returns null on a non-2xx response', async () => {
    globalThis.fetch = async () => ({ ok: false, status: 503, json: async () => ({}) });

    const client = new AgentClient({ fqdn: 'web01.prod.internal', port: 8080 });
    assert.equal(await client.getStatus(), null);
  });

  test('returns null on a network error', async () => {
    globalThis.fetch = async () => {
      throw new Error('connection refused');
    };

    const client = new AgentClient({ fqdn: 'web01.prod.internal', port: 8080 });
    assert.equal(await client.getStatus(), null);
  });

  test('returns null on timeout (abort)', async () => {
    globalThis.fetch = async (_url, { signal }) =>
      new Promise((_resolve, reject) => {
        signal.addEventListener('abort', () => reject(new Error('aborted')));
      });

    const client = new AgentClient({ fqdn: 'web01.prod.internal', port: 8080, timeoutMs: 10 });
    assert.equal(await client.getStatus(), null);
  });
});
