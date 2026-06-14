// Tracks the fleet of enrolled endpoint agents. Currently backed by the
// read-only CSV inventory (see ./inventory.js). As enrollment/management
// features are added, this module can grow beyond a thin pass-through.

import * as inventory from './inventory.js';

export function listAgents() {
  return inventory.listAgents();
}

export function getAgent(id) {
  return inventory.getAgent(id);
}
