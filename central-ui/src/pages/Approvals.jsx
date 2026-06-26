import { useState } from 'react';
import { Link } from 'react-router-dom';
import Badge from '../components/Badge';
import Card from '../components/Card';
import AsyncState from '../components/AsyncState';
import { CheckIcon, XIcon } from '../components/icons';
import { fetchApprovals, decideApproval } from '../api/client';
import { useApi } from '../hooks/useApi';
import { relativeTime } from '../utils/time';

export default function Approvals() {
  const { data: approvals, loading, error } = useApi(fetchApprovals, []);
  // Per-approval UI state: id -> { status, loading, error }
  const [decisions, setDecisions] = useState({});

  const decide = async (entry, decision) => {
    setDecisions((prev) => ({ ...prev, [entry.id]: { status: null, loading: true, error: null } }));
    try {
      await decideApproval(entry.id, decision, entry.agentId);
      setDecisions((prev) => ({ ...prev, [entry.id]: { status: decision, loading: false, error: null } }));
    } catch (err) {
      if (err.message.includes('404') || err.message.toLowerCase().includes('not found')) {
        setDecisions((prev) => ({ ...prev, [entry.id]: { status: 'stale', loading: false, error: null } }));
      } else {
        setDecisions((prev) => ({ ...prev, [entry.id]: { status: null, loading: false, error: err.message } }));
      }
    }
  };

  if (loading || error) {
    return <AsyncState loading={loading} error={error} loadingLabel="Loading approvals..." />;
  }

  const open = approvals.filter((a) => !decisions[a.id]?.status);
  const resolved = approvals.filter((a) => decisions[a.id]?.status);

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold text-black">Approvals</h1>
        <p className="mt-1 text-sm text-navy-500">
          Actions agents have proposed but won't take until you sign off. Sorted by risk.
        </p>
      </div>

      <Card title="Waiting on you" subtitle={`${open.length} request${open.length === 1 ? '' : 's'} across the fleet`}>
        {open.length === 0 ? (
          <p className="text-sm text-navy-500">Nothing waiting on you right now.</p>
        ) : (
          <div className="space-y-4">
            {open.map((entry) => {
              const state = decisions[entry.id];
              return (
                <div key={entry.id} className="rounded-lg border border-navy-200 p-4">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="flex flex-wrap items-center gap-2">
                      <Badge variant={entry.risk}>{entry.risk} risk</Badge>
                      <Link to={`/agents/${entry.agentId}`} className="text-sm font-medium text-navy-600 hover:text-navy-800">
                        {entry.hostname}
                      </Link>
                      <span className="text-xs text-navy-400">requested {relativeTime(entry.time)}</span>
                    </div>
                  </div>
                  <p className="mt-2 text-sm font-medium text-navy-900">{entry.title}</p>
                  <p className="mt-1 text-sm text-navy-500">{entry.detail}</p>
                  <p className="mt-2 rounded-md bg-navy-50 px-3 py-2 font-mono text-xs text-navy-600">
                    {entry.proposedAction}
                  </p>
                  {state?.error && (
                    <p className="mt-2 text-xs text-rose-700">{state.error}</p>
                  )}
                  <div className="mt-3 flex gap-2">
                    <button
                      onClick={() => decide(entry, 'approved')}
                      disabled={state?.loading}
                      className="inline-flex items-center gap-1.5 rounded-lg bg-emerald-500 px-3 py-1.5 text-xs font-semibold text-white hover:bg-emerald-600 disabled:opacity-50 disabled:cursor-not-allowed"
                    >
                      <CheckIcon className="h-3.5 w-3.5" /> Approve
                    </button>
                    <button
                      onClick={() => decide(entry, 'rejected')}
                      disabled={state?.loading}
                      className="inline-flex items-center gap-1.5 rounded-lg border border-navy-300 px-3 py-1.5 text-xs font-semibold text-navy-700 hover:border-rose-400 hover:text-rose-800 disabled:opacity-50 disabled:cursor-not-allowed"
                    >
                      <XIcon className="h-3.5 w-3.5" /> Reject
                    </button>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </Card>

      {resolved.length > 0 && (
        <Card title="Resolved this session" subtitle="The agent will receive the decision within seconds">
          <div className="space-y-3">
            {resolved.map((entry) => (
              <div key={entry.id} className="flex items-center justify-between gap-4 rounded-lg border border-navy-200 p-3">
                <div className="flex items-center gap-3">
                  {decisions[entry.id].status === 'stale'
                    ? <Badge variant="neutral">No longer pending</Badge>
                    : <Badge variant={decisions[entry.id].status}>{decisions[entry.id].status}</Badge>
                  }
                  <div>
                    <p className="text-sm font-medium text-navy-900">{entry.title}</p>
                    <Link to={`/agents/${entry.agentId}`} className="text-xs font-medium text-navy-600 hover:text-navy-800">
                      {entry.hostname}
                    </Link>
                  </div>
                </div>
              </div>
            ))}
          </div>
        </Card>
      )}
    </div>
  );
}
