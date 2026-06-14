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

describe('AgentClient.sendMessage', () => {
  let originalFetch;

  beforeEach(() => {
    originalFetch = globalThis.fetch;
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  test('sends a JSON-RPC SendMessage request and extracts the reply text', async () => {
    let capturedUrl;
    let capturedBody;
    globalThis.fetch = async (url, opts) => {
      capturedUrl = url;
      capturedBody = JSON.parse(opts.body);
      return {
        ok: true,
        json: async () => ({
          jsonrpc: '2.0',
          id: capturedBody.id,
          result: { message: { messageId: 'm2', role: 'ROLE_AGENT', parts: [{ text: 'hello there' }] } },
        }),
      };
    };

    const client = new AgentClient({ fqdn: 'web01.prod.internal', port: 8080 });
    const reply = await client.sendMessage('hi');

    assert.equal(capturedUrl, 'http://web01.prod.internal:8080');
    assert.equal(capturedBody.method, 'SendMessage');
    assert.equal(capturedBody.params.message.parts[0].text, 'hi');
    assert.equal(reply, 'hello there');
  });

  test('extracts reply text from a Task result', async () => {
    globalThis.fetch = async () => ({
      ok: true,
      json: async () => ({
        jsonrpc: '2.0',
        id: '1',
        result: {
          task: {
            id: 't1',
            status: { state: 'completed', message: { parts: [{ text: 'done' }] } },
          },
        },
      }),
    });

    const client = new AgentClient({ fqdn: 'web01.prod.internal', port: 8080 });
    assert.equal(await client.sendMessage('hi'), 'done');
  });

  test('sends an Authorization header when an auth token is configured', async () => {
    let capturedHeaders;
    globalThis.fetch = async (_url, opts) => {
      capturedHeaders = opts.headers;
      return {
        ok: true,
        json: async () => ({ jsonrpc: '2.0', id: '1', result: { message: { parts: [] } } }),
      };
    };

    const client = new AgentClient({ fqdn: 'web01.prod.internal', port: 8080, authToken: 'secret' });
    await client.sendMessage('hi');

    assert.equal(capturedHeaders.Authorization, 'Bearer secret');
  });

  test('throws on a non-2xx response', async () => {
    globalThis.fetch = async () => ({ ok: false, status: 503, statusText: 'Service Unavailable' });

    const client = new AgentClient({ fqdn: 'web01.prod.internal', port: 8080 });
    await assert.rejects(() => client.sendMessage('hi'), /503/);
  });

  test('throws on a JSON-RPC error response', async () => {
    globalThis.fetch = async () => ({
      ok: true,
      json: async () => ({ jsonrpc: '2.0', id: '1', error: { code: -32600, message: 'invalid request' } }),
    });

    const client = new AgentClient({ fqdn: 'web01.prod.internal', port: 8080 });
    await assert.rejects(() => client.sendMessage('hi'), /invalid request/);
  });
});
