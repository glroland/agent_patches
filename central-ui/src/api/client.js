const API_BASE_URL = '/api';

async function getJSON(path) {
  const response = await fetch(`${API_BASE_URL}${path}`);
  if (!response.ok) {
    throw new Error(`${path} returned ${response.status} ${response.statusText}`);
  }
  return response.json();
}

async function postJSON(path, body) {
  const response = await fetch(`${API_BASE_URL}${path}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!response.ok) {
    const payload = await response.json().catch(() => null);
    throw new Error(payload?.message || `${path} returned ${response.status} ${response.statusText}`);
  }
  return response.json();
}

async function deleteJSON(path) {
  const response = await fetch(`${API_BASE_URL}${path}`, { method: 'DELETE' });
  if (!response.ok) {
    const payload = await response.json().catch(() => null);
    throw new Error(payload?.message || `${path} returned ${response.status} ${response.statusText}`);
  }
  return response.json();
}

// Fleet-wide counts for the navigation shell.
export function fetchSummary() {
  return getJSON('/summary');
}

// Everything the Dashboard screen needs: stats, agents needing attention,
// top pending approvals, and recent activity.
export function fetchDashboard() {
  return getJSON('/dashboard');
}

// The Agents screen's fleet list.
export function fetchAgents() {
  return getJSON('/agents');
}

// A single agent's full profile and activity timeline.
export function fetchAgent(id) {
  return getJSON(`/agents/${id}`);
}

// A single agent's current memory: every memory domain's latest snapshot
// plus all attrs.
export function fetchAgentMemory(id) {
  return getJSON(`/agents/${id}/memory`);
}

// Pending approval requests across the fleet.
export function fetchApprovals() {
  return getJSON('/approvals');
}

// Submits an operator decision for a pending approval.
// decision must be "approved" or "rejected".
export function decideApproval(approvalId, decision, agentId, reason = '') {
  return postJSON(`/approvals/${approvalId}/decision`, { decision, agentId, reason });
}

// Aggregated issues/concerns plus per-severity counts.
export function fetchIssues() {
  return getJSON('/issues');
}

// Sends a chat message to an agent and returns its reply.
export function sendAgentMessage(id, message) {
  return postJSON(`/agents/${id}/messages`, { message });
}

// Sends a chat message to every agent in the fleet and returns each
// agent's reply (or error).
export function broadcastMessage(message) {
  return postJSON('/chat', { message });
}

// Clears all memory on a single agent.
export function clearAgentMemory(id) {
  return deleteJSON(`/agents/${id}/memory`);
}

// Clears memory on every enrolled agent in parallel.
export function clearAllAgentsMemory() {
  return deleteJSON('/admin/memory');
}
