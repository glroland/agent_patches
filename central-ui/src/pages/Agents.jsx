import { useState, useMemo } from 'react';
import { Link } from 'react-router-dom';
import Badge from '../components/Badge';
import Card from '../components/Card';
import AsyncState from '../components/AsyncState';
import { SearchIcon, HandIcon } from '../components/icons';
import { useFleetSocket } from '../hooks/useFleetSocket';
import { relativeTime } from '../utils/time';

const STATUS_FILTERS = [
  { value: 'all', label: 'All' },
  { value: 'active', label: 'Active' },
  { value: 'idle', label: 'Idle' },
  { value: 'attention', label: 'Needs attention' },
  { value: 'offline', label: 'Offline' },
];

export default function Agents() {
  const { agents } = useFleetSocket();
  const [query, setQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState('all');

  const newestBuildTime = useMemo(() => {
    if (!agents) return null;
    const times = agents.map((a) => a.buildTime).filter(Boolean).filter((t) => t !== 'dev');
    if (times.length === 0) return null;
    return times.reduce((best, t) => (t > best ? t : best));
  }, [agents]);

  const filtered = useMemo(() => {
    if (!agents) return [];
    return agents.filter((a) => {
      if (statusFilter !== 'all' && a.status !== statusFilter) return false;
      if (!query) return true;
      const q = query.toLowerCase();
      return (
        a.hostname.toLowerCase().includes(q) ||
        a.role.toLowerCase().includes(q) ||
        a.os.toLowerCase().includes(q) ||
        a.tags.some((t) => t.toLowerCase().includes(q))
      );
    });
  }, [agents, query, statusFilter]);

  if (!agents) {
    return <AsyncState loading loadingLabel="Loading agents..." />;
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold text-black">Agents</h1>
          <p className="mt-1 text-sm text-navy-500">{agents.length} agents, each managing one endpoint.</p>
        </div>
        <div className="flex items-center gap-3">
          <div className="relative">
            <SearchIcon className="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-navy-500" />
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search hostname, role, tag, OS..."
              className="w-64 rounded-lg border border-navy-200 bg-white py-2 pl-9 pr-3 text-sm text-navy-900 placeholder:text-navy-400 focus:border-navy-500 focus:outline-none"
            />
          </div>
          <div className="flex rounded-lg border border-navy-200 bg-white p-1 text-xs">
            {STATUS_FILTERS.map(({ value, label }) => (
              <button
                key={value}
                onClick={() => setStatusFilter(value)}
                className={`rounded-md px-3 py-1.5 font-medium capitalize transition-colors ${
                  statusFilter === value ? 'bg-navy-100 text-navy-700' : 'text-navy-600 hover:text-navy-900'
                }`}
              >
                {label}
              </button>
            ))}
          </div>
        </div>
      </div>

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
        {filtered.map((agent) => (
          <Link key={agent.id} to={`/agents/${agent.id}`}>
            <Card className="h-full transition-colors hover:border-navy-400">
              <div className="flex items-start justify-between">
                <div>
                  <p className="font-semibold text-black">{agent.hostname}</p>
                  <p className="text-xs text-navy-500">{agent.role} &middot; {agent.os}</p>
                </div>
                <div className="flex items-center gap-2">
                  {agent.buildTime === 'dev' ? (
                    <span className="rounded-full bg-rose-100 px-2 py-0.5 text-xs font-semibold text-rose-700">Dev Build</span>
                  ) : newestBuildTime && agent.buildTime && agent.buildTime < newestBuildTime ? (
                    <span className="rounded-full bg-rose-100 px-2 py-0.5 text-xs font-semibold text-rose-700">Out of Date</span>
                  ) : null}
                  <Badge variant={agent.status}>{agent.statusLabel}</Badge>
                </div>
              </div>

              <div className="mt-4">
                <p className="text-xs font-medium uppercase tracking-wide text-navy-500">
                  {agent.currentTask ? 'Currently' : 'Status'}
                </p>
                <p className="mt-1 text-sm text-navy-700">
                  {agent.currentTask ?? agent.statusDescription}
                </p>
              </div>

              {agent.latestActivity && (
                <div className="mt-3">
                  <p className="text-xs font-medium uppercase tracking-wide text-navy-500">Latest</p>
                  <p className="mt-1 text-sm text-navy-600 line-clamp-2">{agent.latestActivity.title}</p>
                </div>
              )}

              <div className="mt-4 flex flex-wrap items-center gap-2 text-xs">
                {agent.pendingApprovalCount > 0 && (
                  <Link
                    to={`/agents/${agent.id}?tab=approvals`}
                    onClick={(e) => e.stopPropagation()}
                  >
                    <Badge variant="pending">
                      <HandIcon className="h-3 w-3" /> {agent.pendingApprovalCount} awaiting approval
                    </Badge>
                  </Link>
                )}
                {agent.tags.map((t) => (
                  <span key={t} className="rounded-full bg-navy-100 px-2 py-1 text-navy-600">{t}</span>
                ))}
              </div>

              <p className="mt-4 text-xs text-navy-500">
                Last successfully polled {agent.lastPoll ? relativeTime(agent.lastPoll) : 'never'}
              </p>
            </Card>
          </Link>
        ))}
      </div>

      {filtered.length === 0 && (
        <Card>
          <p className="py-8 text-center text-sm text-navy-500">No agents match your filters.</p>
        </Card>
      )}
    </div>
  );
}
