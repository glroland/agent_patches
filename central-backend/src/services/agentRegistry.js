// Tracks the fleet of enrolled endpoint agents (hostname, address, status,
// etc). Will eventually be backed by a database or config file populated by
// an enrollment process. For now this is an empty in-memory placeholder.

const agents = new Map();

export function listAgents() {
  return Array.from(agents.values());
}

export function getAgent(id) {
  return agents.get(id);
}

export function registerAgent(/* agent */) {
  throw new Error('registerAgent: not implemented');
}
