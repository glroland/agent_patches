import { useState } from 'react';
import { Link } from 'react-router-dom';
import Badge from '../components/Badge';
import Card from '../components/Card';
import StatCard from '../components/StatCard';
import AsyncState from '../components/AsyncState';
import { CheckIcon } from '../components/icons';
import { fetchIssues, resolveIssue } from '../api/client';
import { useApi } from '../hooks/useApi';
import { relativeTime } from '../utils/time';

const SEVERITIES = ['all', 'critical', 'warning', 'info'];

// Synthetic concerns — "agent is offline" (central-backend/services/activity.js)
// and skill health snapshots (endpoint-server/status/status.go, id prefix
// "skillstate:") — aren't real timeline findings. They're recomputed fresh on
// every GET /status rather than stored, so there's nothing on the agent side
// for a resolve call to find; they clear on their own once the underlying
// condition (agent reachable, skill healthy) recovers.
const isDismissable = (item) => !item.id.endsWith('-offline') && !item.id.startsWith('skillstate:');

export default function Issues() {
  const { data, loading, error } = useApi(fetchIssues, []);
  const [severity, setSeverity] = useState('all');
  // Per-item UI state for the resolve action: id -> { error }
  const [resolving, setResolving] = useState({});
  const [resolvedIds, setResolvedIds] = useState(() => new Set());

  if (loading || error) {
    return <AsyncState loading={loading} error={error} loadingLabel="Loading issues..." />;
  }

  const { items, counts } = data;

  // Optimistic: hide the item the instant it's clicked rather than making the
  // operator wait on a live round-trip to the remote agent host. Only put it
  // back (with an error) if the call actually fails.
  const resolve = async (item) => {
    setResolvedIds((prev) => new Set(prev).add(item.id));
    setResolving((prev) => ({ ...prev, [item.id]: { error: null } }));
    try {
      await resolveIssue(item.id, item.agentId);
    } catch (err) {
      setResolvedIds((prev) => {
        const next = new Set(prev);
        next.delete(item.id);
        return next;
      });
      setResolving((prev) => ({ ...prev, [item.id]: { error: err.message } }));
    }
  };

  const visible = items.filter((item) => !resolvedIds.has(item.id));
  const filtered = visible.filter((item) => severity === 'all' || item.severity === severity);

  // Reflect this-session resolutions in the summary counts without waiting
  // for the next poll cycle to refetch from central-backend.
  const displayCounts = { ...counts };
  for (const id of resolvedIds) {
    const item = items.find((i) => i.id === id);
    if (item) displayCounts[item.severity] = Math.max(0, (displayCounts[item.severity] ?? 0) - 1);
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold text-black">Issues & Concerns</h1>
        <p className="mt-1 text-sm text-navy-500">Things your agents have flagged for awareness, in their own words.</p>
      </div>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <StatCard label="Critical" value={displayCounts.critical} tone={displayCounts.critical > 0 ? 'danger' : 'success'} hint="Needs attention soon" />
        <StatCard label="Warning" value={displayCounts.warning} tone={displayCounts.warning > 0 ? 'warning' : 'success'} hint="Worth a look" />
        <StatCard label="Informational" value={displayCounts.info} hint="For awareness only" />
      </div>

      <div className="flex rounded-lg border border-navy-200 bg-white p-1 text-xs w-fit">
        {SEVERITIES.map((s) => (
          <button
            key={s}
            onClick={() => setSeverity(s)}
            className={`rounded-md px-3 py-1.5 font-medium capitalize transition-colors ${
              severity === s ? 'bg-navy-100 text-navy-700' : 'text-navy-600 hover:text-navy-900'
            }`}
          >
            {s}
          </button>
        ))}
      </div>

      <Card>
        {filtered.length === 0 ? (
          <p className="py-8 text-center text-sm text-navy-500">No concerns match the selected filter.</p>
        ) : (
          <div className="divide-y divide-navy-200">
            {filtered.map((item) => {
              const state = resolving[item.id];
              return (
                <div key={item.id} className="flex items-start justify-between gap-4 py-4 first:pt-0 last:pb-0">
                  <div className="flex items-start gap-3">
                    <Badge variant={item.severity}>{item.severity}</Badge>
                    <div>
                      <p className="text-sm font-medium text-navy-900">{item.title}</p>
                      <p className="mt-0.5 text-sm text-navy-500">{item.detail}</p>
                      <p className="mt-1.5 text-xs text-navy-400">flagged {relativeTime(item.time)}</p>
                      {state?.error && <p className="mt-1 text-xs text-rose-700">{state.error}</p>}
                    </div>
                  </div>
                  <div className="flex flex-shrink-0 items-center gap-2">
                    {isDismissable(item) && (
                      <button
                        onClick={() => resolve(item)}
                        title="Mark resolved"
                        className="inline-flex items-center gap-1.5 whitespace-nowrap rounded-lg border border-navy-300 px-3 py-1.5 text-xs font-semibold text-navy-700 hover:border-emerald-400 hover:text-emerald-700"
                      >
                        <CheckIcon className="h-3.5 w-3.5" /> Resolve
                      </button>
                    )}
                    <Link
                      to={`/agents/${item.agentId}`}
                      className="whitespace-nowrap rounded-lg border border-navy-300 px-3 py-1.5 text-xs font-medium text-navy-700 hover:border-navy-400 hover:text-navy-800"
                    >
                      {item.hostname}
                    </Link>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </Card>
    </div>
  );
}
