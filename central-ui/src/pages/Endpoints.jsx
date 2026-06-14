import { useState, useMemo } from 'react';
import { Link } from 'react-router-dom';
import Badge from '../components/Badge';
import Card from '../components/Card';
import ProgressBar from '../components/ProgressBar';
import { SearchIcon } from '../components/icons';
import { endpoints, usedPct, memUsedPct, formatBytes } from '../data/endpoints';

const STATUS_FILTERS = ['all', 'online', 'degraded', 'offline'];

export default function Endpoints() {
  const [query, setQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState('all');

  const filtered = useMemo(() => {
    return endpoints.filter((e) => {
      if (statusFilter !== 'all' && e.status !== statusFilter) return false;
      if (!query) return true;
      const q = query.toLowerCase();
      return (
        e.hostname.toLowerCase().includes(q) ||
        e.ip.includes(q) ||
        e.tags.some((t) => t.toLowerCase().includes(q)) ||
        e.os.toLowerCase().includes(q)
      );
    });
  }, [query, statusFilter]);

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold text-slate-100">Endpoints</h1>
          <p className="mt-1 text-sm text-slate-500">{endpoints.length} agents enrolled in this fleet.</p>
        </div>
        <div className="flex items-center gap-3">
          <div className="relative">
            <SearchIcon className="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-slate-500" />
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search hostname, IP, tag, OS..."
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
                {s}
              </button>
            ))}
          </div>
        </div>
      </div>

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
        {filtered.map((e) => {
          const maxDisk = Math.max(...e.disks.map(usedPct));
          const smartFail = e.disks.some((d) => d.smart?.status === 'FAILED');
          return (
            <Link key={e.id} to={`/endpoints/${e.id}`}>
              <Card className="h-full transition-colors hover:border-indigo-500/50">
                <div className="flex items-start justify-between">
                  <div>
                    <p className="font-semibold text-slate-100">{e.hostname}</p>
                    <p className="text-xs text-slate-500">{e.ip} &middot; {e.os} {e.osVersion}</p>
                  </div>
                  <Badge variant={e.status}>{e.status}</Badge>
                </div>

                {e.statusNote && (
                  <p className="mt-2 rounded-md bg-slate-800/60 px-2.5 py-1.5 text-xs text-amber-300">{e.statusNote}</p>
                )}

                <div className="mt-4 space-y-3">
                  <ProgressBar pct={maxDisk} label="Peak disk usage" />
                  <ProgressBar pct={memUsedPct(e.memory)} label="Memory" />
                </div>

                <div className="mt-4 flex flex-wrap items-center gap-2 text-xs">
                  {e.patches.total > 0 ? (
                    <Badge variant={e.patches.security > 0 ? 'warning' : 'info'}>
                      {e.patches.total} update{e.patches.total > 1 ? 's' : ''}{e.patches.security > 0 ? ` (${e.patches.security} sec)` : ''}
                    </Badge>
                  ) : (
                    <Badge variant="online">Up to date</Badge>
                  )}
                  {smartFail && <Badge variant="critical">SMART failure</Badge>}
                  {e.logins.length > 0 && <Badge variant="neutral">{e.logins.length} active session{e.logins.length > 1 ? 's' : ''}</Badge>}
                  {e.tags.map((t) => (
                    <span key={t} className="rounded-full bg-slate-800 px-2 py-1 text-slate-400">{t}</span>
                  ))}
                </div>

                <p className="mt-4 text-xs text-slate-500">
                  Agent v{e.agentVersion} &middot; {formatBytes(e.memory.total)} RAM &middot; last check-in{' '}
                  {new Date(e.lastCheckIn).toLocaleTimeString()}
                </p>
              </Card>
            </Link>
          );
        })}
      </div>

      {filtered.length === 0 && (
        <Card>
          <p className="py-8 text-center text-sm text-slate-500">No endpoints match your filters.</p>
        </Card>
      )}
    </div>
  );
}
