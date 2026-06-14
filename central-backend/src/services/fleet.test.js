import { test, describe, before, after, beforeEach } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

let listFleet;
let AgentClient;
let _clearStatusCacheForTests;
let inventoryPath;

before(async () => {
  inventoryPath = path.join(os.tmpdir(), `fleet-test-inventory-${process.pid}.csv`);
  fs.writeFileSync(
    inventoryPath,
    'display_name,fqdn,port,os_type\n' +
      'web01,web01.prod.internal,8080,ubuntu\n' +
      'build01,build01.ci.internal,8080,fedora\n'
  );
  process.env.AGENT_INVENTORY_FILE = inventoryPath;

  ({ listFleet, _clearStatusCacheForTests } = await import('./fleet.js'));
  ({ AgentClient } = await import('./agentClient.js'));
});

after(() => {
  fs.rmSync(inventoryPath, { force: true });
});

describe('fleet.listFleet', () => {
  let originalGetStatus;

  beforeEach(() => {
    originalGetStatus = AgentClient.prototype.getStatus;
    _clearStatusCacheForTests();
  });

  test('merges live /status data for a reachable agent', async (t) => {
    t.after(() => {
      AgentClient.prototype.getStatus = originalGetStatus;
    });

    AgentClient.prototype.getStatus = async function () {
      if (this.baseUrl === 'http://web01.prod.internal:8080') {
        return {
          agent: { hostname: 'web01.prod.internal', platform: 'linux', os: 'Ubuntu 24.04', role: 'Frontend web server', tags: ['production'] },
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
    assert.deepEqual(web01.tags, ['production']);
    assert.equal(web01.currentTask, 'patching');
    assert.equal(web01.timeline.length, 1);
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

  test('caches /status responses within the TTL window', async (t) => {
    t.after(() => {
      AgentClient.prototype.getStatus = originalGetStatus;
    });

    let calls = 0;
    AgentClient.prototype.getStatus = async function () {
      calls += 1;
      return { agent: {}, status: { state: 'idle', lastPoll: null, currentTask: null }, timeline: [] };
    };

    await listFleet();
    await listFleet();

    // Two agents in inventory; each should only be fetched once across both
    // listFleet() calls thanks to the short-TTL cache.
    assert.equal(calls, 2);
  });
});
