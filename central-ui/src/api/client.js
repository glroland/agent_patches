const API_BASE_URL = import.meta.env.VITE_API_BASE_URL || 'http://localhost:4000/api';

async function getJSON(path) {
  const response = await fetch(`${API_BASE_URL}${path}`);
  if (!response.ok) {
    throw new Error(`${path} returned ${response.status} ${response.statusText}`);
  }
  return response.json();
}

// Returns the fleet inventory from central-backend:
// [{ id, displayName, fqdn, port, osFlavor }]
export function fetchAgentInventory() {
  return getJSON('/agents');
}
