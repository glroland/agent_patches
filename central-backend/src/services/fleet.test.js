import { test, describe, before, after, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

let listFleet;
let sendAgentMessage;
let broadcastMessage;
let setFleet;
let AgentClient;
let inventoryPath;

before(async () => {
  process.env.AGENT_AUTH_TOKEN = 'test-token';

  inventoryPath = path.join(os.tmpdir(), `fleet-test-inventory-${process.pid}.csv`);
  fs.writeFileSync(
    inventoryPath,
    'display_name,fqdn,port,os_type,role,tags\n' +
      'web01,web01.prod.internal,8080,ubuntu,Frontend web server,"production,frontend"\n' +
      'build01,build01.ci.internal,8080,fedora,,\n'
  );
  process.env.AGENT_INVENTORY_FILE = inventoryPath;

  ({ listFleet, sendAgentMessage, broadcastMessage } = await import('./fleet.js'));
  ({ setFleet } = await import('./fleetCache.js'));
  ({ AgentClient } = await import('./agentClient.js'));
});

after(() => {
  fs.rmSync(inventoryPath, { force: true });
});

describe('fleet.listFleet', () => {
  let originalGetStatus;

  beforeEach(() => {
    originalGetStatus = AgentClient.prototype.getStatus;
    // Reset the fleet cache so each test starts with a live-fetch fallback.
    setFleet(null);
  });

  test('merges live /status data for a reachable agent', async (t) => {
    t.after(() => {
      AgentClient.prototype.getStatus = originalGetStatus;
    });

    AgentClient.prototype.getStatus = async function () {
      if (this.baseUrl === 'http://web01.prod.internal:8080') {
        return {
          agent: {
            hostname: 'web01.prod.internal',
            platform: 'linux',
            os: 'Ubuntu 24.04',
            purpose: 'Customer-facing web traffic',
          },
          status: { state: 'active', lastPoll: '2026-06-14T08:00:00Z', currentTask: 'patching' },
          timeline: [{ id: '1', time: '2026-06-14T08:00:00Z', type: 'observation', title: 'ok', detail: 'ok' }],
        };
      }
      return null;
    };

    const agents = await listFleet();
    const web01 = agents.find((a) => a.id === 'web01');

    assert.equal(web01.status, 'active');
    assert.equal(web01.statusLabel, 'Active');
    assert.equal(web01.os, 'Ubuntu 24.04');
    assert.equal(web01.role, 'Frontend web server');
    assert.deepEqual(web01.tags, ['production', 'frontend']);
    assert.equal(web01.currentTask, 'patching');
    assert.equal(web01.timeline.length, 1);
    assert.equal(web01.purpose, 'Customer-facing web traffic');
  });

  test('falls back to offline when the agent is unreachable', async (t) => {
    t.after(() => {
      AgentClient.prototype.getStatus = originalGetStatus;
    });

    AgentClient.prototype.getStatus = async () => null;

    const agents = await listFleet();
    const build01 = agents.find((a) => a.id === 'build01');

    assert.equal(build01.status, 'offline');
    assert.equal(build01.statusLabel, 'Offline');
    assert.equal(build01.lastPoll, null);
    assert.equal(build01.currentTask, null);
    assert.deepEqual(build01.timeline, []);
    assert.equal(build01.os, 'fedora'); // falls back to inventory osType
    assert.equal(build01.role, 'Endpoint agent');
  });

  test('keeps the last successful poll time once an agent goes offline', async (t) => {
    t.after(() => {
      AgentClient.prototype.getStatus = originalGetStatus;
    });

    AgentClient.prototype.getStatus = async function () {
      if (this.baseUrl === 'http://web01.prod.internal:8080') {
        return { agent: {}, status: { state: 'idle' }, timeline: [] };
      }
      return null;
    };

    setFleet(null);
    const firstPoll = await listFleet();
    const seenLastPoll = firstPoll.find((a) => a.id === 'web01').lastPoll;
    assert.ok(seenLastPoll);

    AgentClient.prototype.getStatus = async () => null;

    setFleet(null);
    const secondPoll = await listFleet();
    const web01AfterDrop = secondPoll.find((a) => a.id === 'web01');

    assert.equal(web01AfterDrop.status, 'offline');
    // lastPoll should still reflect the earlier successful poll, not null —
    // otherwise an agent that just went offline looks like it was never seen.
    assert.equal(web01AfterDrop.lastPoll, seenLastPoll);
  });

  test('returns fleet from cache without calling agents when cache is populated', async (t) => {
    t.after(() => {
      AgentClient.prototype.getStatus = originalGetStatus;
      setFleet(null);
    });

    const cached = [{ id: 'web01', status: 'idle', hostname: 'web01.prod.internal' }];
    setFleet(cached);

    let calls = 0;
    AgentClient.prototype.getStatus = async () => {
      calls += 1;
      return { agent: {}, status: { state: 'idle', lastPoll: null, currentTask: null }, timeline: [] };
    };

    const agents = await listFleet();
    assert.equal(calls, 0);
    assert.deepEqual(agents, cached);
  });
});

describe('fleet.sendAgentMessage', () => {
  let originalSendMessage;

  beforeEach(() => {
    originalSendMessage = AgentClient.prototype.sendMessage;
  });

  afterEach(() => {
    AgentClient.prototype.sendMessage = originalSendMessage;
  });

  test('relays the message to the agent and returns its reply', async () => {
    AgentClient.prototype.sendMessage = async function (text) {
      assert.equal(this.baseUrl, 'http://web01.prod.internal:8080');
      assert.equal(text, 'what are you doing?');
      return 'Currently idle.';
    };

    const reply = await sendAgentMessage('web01', 'what are you doing?');
    assert.equal(reply, 'Currently idle.');
  });

  test('returns undefined for an unknown agent id', async () => {
    const reply = await sendAgentMessage('does-not-exist', 'hi');
    assert.equal(reply, undefined);
  });
});

describe('fleet.broadcastMessage', () => {
  let originalSendMessage;

  beforeEach(() => {
    originalSendMessage = AgentClient.prototype.sendMessage;
  });

  afterEach(() => {
    AgentClient.prototype.sendMessage = originalSendMessage;
  });

  test('sends to every agent and returns a result per agent, errors included', async () => {
    AgentClient.prototype.sendMessage = async function (text) {
      assert.equal(text, 'status?');
      if (this.baseUrl === 'http://web01.prod.internal:8080') {
        return 'all good';
      }
      throw new Error('connection refused');
    };

    const results = await broadcastMessage('status?');

    const web01 = results.find((r) => r.id === 'web01');
    const build01 = results.find((r) => r.id === 'build01');

    assert.equal(web01.reply, 'all good');
    assert.equal(web01.error, undefined);

    assert.equal(build01.reply, undefined);
    assert.equal(build01.error, 'connection refused');
  });
});
