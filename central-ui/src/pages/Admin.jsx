import { useState } from 'react';
import { useApi } from '../hooks/useApi';
import { fetchAgents, clearAgentMemory, clearAllAgentsMemory } from '../api/client';
import AsyncState from '../components/AsyncState';
import Card from '../components/Card';
import { TrashIcon } from '../components/icons';

function ConfirmButton({ label, description, onConfirm, danger = true }) {
  const [state, setState] = useState('idle'); // idle | confirm | working | done | error
  const [errorMsg, setErrorMsg] = useState('');

  const run = async () => {
    setState('working');
    try {
      await onConfirm();
      setState('done');
      setTimeout(() => setState('idle'), 3000);
    } catch (err) {
      setErrorMsg(err.message);
      setState('error');
      setTimeout(() => setState('idle'), 4000);
    }
  };

  if (state === 'confirm') {
    return (
      <div className="flex items-center gap-2">
        <span className="text-xs text-navy-600">Are you sure?</span>
        <button
          onClick={run}
          className="inline-flex items-center gap-1.5 rounded-lg bg-rose-600 px-3 py-1.5 text-xs font-semibold text-white hover:bg-rose-700"
        >
          Yes, clear
        </button>
        <button
          onClick={() => setState('idle')}
          className="rounded-lg border border-navy-300 px-3 py-1.5 text-xs text-navy-600 hover:text-navy-900"
        >
          Cancel
        </button>
      </div>
    );
  }

  if (state === 'working') {
    return <span className="text-xs text-navy-500">Clearing...</span>;
  }

  if (state === 'done') {
    return <span className="text-xs text-emerald-700">Cleared</span>;
  }

  if (state === 'error') {
    return <span className="text-xs text-rose-700">{errorMsg}</span>;
  }

  return (
    <button
      onClick={() => setState('confirm')}
      className={`inline-flex items-center gap-1.5 rounded-lg border px-3 py-1.5 text-xs font-semibold transition-colors ${
        danger
          ? 'border-rose-300 text-rose-700 hover:border-rose-600 hover:bg-rose-50'
          : 'border-navy-300 text-navy-600 hover:border-navy-500 hover:text-navy-900'
      }`}
    >
      <TrashIcon className="h-3.5 w-3.5" />
      {label}
    </button>
  );
}

export default function Admin() {
  const { data: agents, loading, error } = useApi(fetchAgents, []);

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold text-black">Admin</h1>
        <p className="mt-1 text-sm text-navy-500">
          Administrative actions across the fleet. Memory cleared here takes effect immediately — agents will rebuild it on their next run cycle.
        </p>
      </div>

      <Card
        title="Fleet-wide memory"
        subtitle="Clear cached timeline, skill state, and attrs on every enrolled agent at once"
      >
        <div className="flex items-center justify-between rounded-lg border border-navy-200 p-4">
          <div>
            <p className="text-sm font-medium text-navy-900">Clear all agent memory</p>
            <p className="mt-0.5 text-xs text-navy-500">
              Removes timeline snapshots, skill state, and attrs on every agent. Useful after a code update that changes output formats.
            </p>
          </div>
          <ConfirmButton
            label="Clear all"
            onConfirm={clearAllAgentsMemory}
          />
        </div>
      </Card>

      <Card
        title="Per-agent memory"
        subtitle="Clear memory on individual agents"
      >
        {loading && <AsyncState loading loadingLabel="Loading agents..." />}
        {error && <AsyncState error={error} />}
        {agents && (
          <div className="space-y-2">
            {agents.map((agent) => (
              <div key={agent.id} className="flex items-center justify-between rounded-lg border border-navy-200 p-3">
                <div>
                  <p className="text-sm font-medium text-navy-900">{agent.hostname}</p>
                  <p className="mt-0.5 text-xs text-navy-500">{agent.role} &middot; {agent.os}</p>
                </div>
                <ConfirmButton
                  label="Clear memory"
                  onConfirm={() => clearAgentMemory(agent.id)}
                />
              </div>
            ))}
          </div>
        )}
      </Card>
    </div>
  );
}
