// Merges the read-only CSV inventory with each agent's activity (currently
// mocked, see ../data/mockActivity.js) into the full fleet view consumed by
// the other services/controllers.

import * as inventory from './inventory.js';
import { mockActivityByHost, defaultActivity, STATUS_META } from '../data/mockActivity.js';

function shortHost(fqdn) {
  return fqdn.split('.')[0].toLowerCase();
}

function toFleetAgent(inventoryAgent) {
  const id = shortHost(inventoryAgent.fqdn);
  const activity = mockActivityByHost[id] || defaultActivity();
  const statusMeta = STATUS_META[activity.status];

  return {
    id,
    hostname: inventoryAgent.fqdn,
    displayName: inventoryAgent.displayName,
    port: inventoryAgent.port,
    osType: inventoryAgent.osType,
    os: activity.os || inventoryAgent.osType,
    role: activity.role,
    tags: activity.tags,
    status: activity.status,
    statusLabel: statusMeta.label,
    statusDescription: statusMeta.description,
    lastPoll: activity.lastPoll,
    currentTask: activity.currentTask,
    timeline: activity.timeline,
  };
}

// Returns the full fleet: inventory rows merged with activity data.
export function listFleet() {
  return inventory.listAgents().map(toFleetAgent);
}

// Returns a single fleet agent by id (short hostname), or undefined if not found.
export function getFleetAgent(id) {
  return listFleet().find((agent) => agent.id === id);
}
