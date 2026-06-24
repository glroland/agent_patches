import { useState } from 'react';
import { Link } from 'react-router-dom';
import Badge from '../components/Badge';
import Card from '../components/Card';
import StatCard from '../components/StatCard';
import AsyncState from '../components/AsyncState';
import { fetchIssues } from '../api/client';
import { useApi } from '../hooks/useApi';
import { relativeTime } from '../utils/time';

const SEVERITIES = ['all', 'critical', 'warning', 'info'];

export default function Issues() {
  const { data, loading, error } = useApi(fetchIssues, []);
  const [severity, setSeverity] = useState('all');

  if (loading || error) {
    return <AsyncState loading={loading} error={error} loadingLabel="Loading issues..." />;
  }

  const { items, counts } = data;
  const filtered = items.filter((c) => severity === 'all' || c.severity === severity);

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold text-black">Issues & Concerns</h1>
        <p className="mt-1 text-sm text-navy-500">Things your agents have flagged for awareness, in their own words.</p>
      </div>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <StatCard label="Critical" value={counts.critical} tone={counts.critical > 0 ? 'danger' : 'success'} hint="Needs attention soon" />
        <StatCard label="Warning" value={counts.warning} tone={counts.warning > 0 ? 'warning' : 'success'} hint="Worth a look" />
        <StatCard label="Informational" value={counts.info} hint="For awareness only" />
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
            {filtered.map((item) => (
              <div key={item.id} className="flex items-start justify-between gap-4 py-4 first:pt-0 last:pb-0">
                <div className="flex items-start gap-3">
                  <Badge variant={item.severity}>{item.severity}</Badge>
                  <div>
                    <p className="text-sm font-medium text-navy-900">{item.title}</p>
                    <p className="mt-0.5 text-sm text-navy-500">{item.detail}</p>
                    <p className="mt-1.5 text-xs text-navy-400">flagged {relativeTime(item.time)}</p>
                  </div>
                </div>
                <Link
                  to={`/agents/${item.agentId}`}
                  className="whitespace-nowrap rounded-lg border border-navy-300 px-3 py-1.5 text-xs font-medium text-navy-700 hover:border-navy-400 hover:text-navy-800"
                >
                  {item.hostname}
                </Link>
              </div>
            ))}
          </div>
        )}
      </Card>
    </div>
  );
}
