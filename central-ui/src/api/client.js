const API_BASE_URL = import.meta.env.VITE_API_BASE_URL || 'http://localhost:4000/api';

async function getJSON(path) {
  const response = await fetch(`${API_BASE_URL}${path}`);
  if (!response.ok) {
    throw new Error(`${path} returned ${response.status} ${response.statusText}`);
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

// Pending approval requests across the fleet.
export function fetchApprovals() {
  return getJSON('/approvals');
}

// Aggregated issues/concerns plus per-severity counts.
export function fetchIssues() {
  return getJSON('/issues');
}
