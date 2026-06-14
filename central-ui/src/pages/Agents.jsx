import { useState, useMemo } from 'react';
import { Link, useOutletContext } from 'react-router-dom';
import Badge from '../components/Badge';
import Card from '../components/Card';
import { SearchIcon, HandIcon } from '../components/icons';
import { STATUS_META } from '../data/agents';
import { relativeTime } from '../utils/time';

const STATUS_FILTERS = ['all', 'active', 'idle', 'attention', 'offline'];

export default function Agents() {
  const { agents } = useOutletContext();
  const [query, setQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState('all');

  const filtered = useMemo(() => {
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

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold text-slate-100">Agents</h1>
          <p className="mt-1 text-sm text-slate-500">{agents.length} agents, each managing one endpoint.</p>
        </div>
        <div className="flex items-center gap-3">
          <div className="relative">
            <SearchIcon className="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-slate-500" />
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search hostname, role, tag, OS..."
              className="w-64 rounded-lg border border-slate-800 bg-slate-900/60 py-2 pl-9 pr-3 text-sm text-slate-200 placeholder:text-slate-500 focus:border-indigo-500 focus:outline-none"
            />
          </div>
          <div className="flex rounded-lg border border-slate-800 bg-slate-900/60 p-1 text-xs">
            {STATUS_FILTERS.map((s) => (
              <button
                key={s}
                onClick={() => setStatusFilter(s)}
                className={`rounded-md px-3 py-1.5 font-medium capitalize transition-colors ${
                  statusFilter === s ? 'bg-indigo-500/20 text-indigo-300' : 'text-slate-400 hover:text-slate-200'
                }`}
              >
                {s === 'all' ? 'All' : STATUS_META[s].label}
              </button>
            ))}
          </div>
        </div>
      </div>

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
        {filtered.map((agent) => {
          const pendingCount = agent.timeline.filter((t) => t.type === 'approval' && t.status === 'pending').length;
          const latest = agent.timeline[0];
          return (
            <Link key={agent.id} to={`/agents/${agent.id}`}>
              <Card className="h-full transition-colors hover:border-indigo-500/50">
                <div className="flex items-start justify-between">
                  <div>
                    <p className="font-semibold text-slate-100">{agent.hostname}</p>
                    <p className="text-xs text-slate-500">{agent.role} &middot; {agent.os}</p>
                  </div>
                  <Badge variant={agent.status}>{STATUS_META[agent.status].label}</Badge>
                </div>

                <div className="mt-4">
                  <p className="text-xs font-medium uppercase tracking-wide text-slate-500">
                    {agent.currentTask ? 'Currently' : 'Status'}
                  </p>
                  <p className="mt-1 text-sm text-slate-300">
                    {agent.currentTask ?? STATUS_META[agent.status].description}
                  </p>
                </div>

                {latest && (
                  <div className="mt-3">
                    <p className="text-xs font-medium uppercase tracking-wide text-slate-500">Latest</p>
                    <p className="mt-1 text-sm text-slate-400 line-clamp-2">{latest.title}</p>
                  </div>
                )}

                <div className="mt-4 flex flex-wrap items-center gap-2 text-xs">
                  {pendingCount > 0 && (
                    <Badge variant="pending">
                      <HandIcon className="h-3 w-3" /> {pendingCount} awaiting approval
                    </Badge>
                  )}
                  {agent.tags.map((t) => (
                    <span key={t} className="rounded-full bg-slate-800 px-2 py-1 text-slate-400">{t}</span>
                  ))}
                </div>

                <p className="mt-4 text-xs text-slate-500">Last polled {relativeTime(agent.lastPoll)}</p>
              </Card>
            </Link>
          );
        })}
      </div>

      {filtered.length === 0 && (
        <Card>
          <p className="py-8 text-center text-sm text-slate-500">No agents match your filters.</p>
        </Card>
      )}
    </div>
  );
}
