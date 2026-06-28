import { getToken, clearToken } from '../auth/oauth.js';

const API_BASE_URL = '/api';

function authHeaders() {
  const token = getToken();
  return token ? { Authorization: `Bearer ${token}` } : {};
}

function handleUnauth() {
  clearToken();
  window.location.href = '/login';
}

async function getJSON(path) {
  const response = await fetch(`${API_BASE_URL}${path}`, {
    headers: authHeaders(),
  });
  if (response.status === 401) { handleUnauth(); return; }
  if (!response.ok) {
    throw new Error(`${path} returned ${response.status} ${response.statusText}`);
  }
  return response.json();
}

async function postJSON(path, body) {
  const response = await fetch(`${API_BASE_URL}${path}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders() },
    body: JSON.stringify(body),
  });
  if (response.status === 401) { handleUnauth(); return; }
  if (!response.ok) {
    const payload = await response.json().catch(() => null);
    throw new Error(payload?.message || `${path} returned ${response.status} ${response.statusText}`);
  }
  return response.json();
}

async function deleteJSON(path) {
  const response = await fetch(`${API_BASE_URL}${path}`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (response.status === 401) { handleUnauth(); return; }
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

// A single agent's enabled responsibilities with schedule, last-run state, and
// next scheduled run time.
export function fetchAgentResponsibilities(id) {
  return getJSON(`/agents/${id}/responsibilities`);
}

// Tailed log content from the agent's log file.
export function fetchAgentLog(id) {
  return getJSON(`/agents/${id}/log`);
}

// A2A agent card served by the agent at /.well-known/agent-card.json.
export function fetchAgentCard(id) {
  return getJSON(`/agents/${id}/card`);
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

// Submits the operator's output for a pending manual-run request.
// status must be "completed" (with output) or "skipped".
export function submitManualRunResult(manualRunId, output, status, agentId) {
  return postJSON(`/manual-runs/${manualRunId}/result`, { output, status, agentId });
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

// Sends a message to the central fleet AI. Returns { reply, routedTo?, agentReply? }.
// history is an array of { role: 'user'|'assistant', text } conversation turns.
export function sendCentralChat(message, history = []) {
  return postJSON('/chat/central', { message, history });
}

// Clears all memory on a single agent.
export function clearAgentMemory(id) {
  return deleteJSON(`/agents/${id}/memory`);
}

// Clears memory on every enrolled agent in parallel.
export function clearAllAgentsMemory() {
  return deleteJSON('/admin/memory');
}

// Token usage, queue depth, and per-endpoint stats from the LLM gateway.
export function fetchGatewayStats() {
  return getJSON('/gateway/stats');
}
