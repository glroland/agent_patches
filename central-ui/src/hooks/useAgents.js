import { useEffect, useState } from 'react';
import { fetchAgentInventory } from '../api/client';
import { mockActivityByHost, defaultActivity } from '../data/mockActivity';

function shortHost(fqdn) {
  return fqdn.split('.')[0].toLowerCase();
}

function mergeAgent(inventoryAgent) {
  const host = shortHost(inventoryAgent.fqdn);
  const activity = mockActivityByHost[host] || defaultActivity();

  return {
    id: host,
    hostname: inventoryAgent.fqdn,
    displayName: inventoryAgent.displayName,
    port: inventoryAgent.port,
    osFlavor: inventoryAgent.osFlavor,
    os: activity.os || inventoryAgent.osFlavor,
    role: activity.role,
    tags: activity.tags,
    status: activity.status,
    lastPoll: activity.lastPoll,
    currentTask: activity.currentTask,
    timeline: activity.timeline,
  };
}

// Fetches the agent inventory from central-backend and merges it with local
// mock activity (observations, actions, recommendations, approvals) keyed by
// short hostname. Returns { agents, loading, error }.
export function useAgents() {
  const [agents, setAgents] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  useEffect(() => {
    let cancelled = false;

    setLoading(true);
    setError(null);

    fetchAgentInventory()
      .then((inventory) => {
        if (cancelled) return;
        setAgents(inventory.map(mergeAgent));
      })
      .catch((err) => {
        if (cancelled) return;
        setError(err);
      })
      .finally(() => {
        if (cancelled) return;
        setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, []);

  return { agents, loading, error };
}
