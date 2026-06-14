import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import Badge from '../components/Badge';
import Card from '../components/Card';
import StatCard from '../components/StatCard';
import { computeIssues } from '../data/issues';

const SEVERITIES = ['all', 'critical', 'warning', 'info'];
const CATEGORIES = ['all', 'connectivity', 'disk', 'hardware', 'patching', 'memory', 'access'];

const CATEGORY_LABELS = {
  connectivity: 'Connectivity',
  disk: 'Disk',
  hardware: 'Hardware',
  patching: 'Patching',
  memory: 'Memory',
  access: 'Access',
};

export default function Issues() {
  const allIssues = useMemo(() => computeIssues(), []);
  const [severity, setSeverity] = useState('all');
  const [category, setCategory] = useState('all');

  const filtered = allIssues.filter((i) => {
    if (severity !== 'all' && i.severity !== severity) return false;
    if (category !== 'all' && i.category !== category) return false;
    return true;
  });

  const critical = allIssues.filter((i) => i.severity === 'critical').length;
  const warning = allIssues.filter((i) => i.severity === 'warning').length;
  const info = allIssues.filter((i) => i.severity === 'info').length;

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold text-slate-100">Issues & Concerns</h1>
        <p className="mt-1 text-sm text-slate-500">Aggregated findings across the fleet, grouped by severity and category.</p>
      </div>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <StatCard label="Critical" value={critical} tone={critical > 0 ? 'danger' : 'success'} hint="Needs immediate attention" />
        <StatCard label="Warning" value={warning} tone={warning > 0 ? 'warning' : 'success'} hint="Should be reviewed soon" />
        <StatCard label="Informational" value={info} hint="For awareness" />
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <div className="flex rounded-lg border border-slate-800 bg-slate-900/60 p-1 text-xs">
          {SEVERITIES.map((s) => (
            <button
              key={s}
              onClick={() => setSeverity(s)}
              className={`rounded-md px-3 py-1.5 font-medium capitalize transition-colors ${
                severity === s ? 'bg-indigo-500/20 text-indigo-300' : 'text-slate-400 hover:text-slate-200'
              }`}
            >
              {s}
            </button>
          ))}
        </div>
        <div className="flex flex-wrap rounded-lg border border-slate-800 bg-slate-900/60 p-1 text-xs">
          {CATEGORIES.map((c) => (
            <button
              key={c}
              onClick={() => setCategory(c)}
              className={`rounded-md px-3 py-1.5 font-medium capitalize transition-colors ${
                category === c ? 'bg-indigo-500/20 text-indigo-300' : 'text-slate-400 hover:text-slate-200'
              }`}
            >
              {c === 'all' ? 'All categories' : CATEGORY_LABELS[c]}
            </button>
          ))}
        </div>
      </div>

      <Card>
        {filtered.length === 0 ? (
          <p className="py-8 text-center text-sm text-slate-500">No issues match the selected filters.</p>
        ) : (
          <div className="divide-y divide-slate-800">
            {filtered.map((issue) => (
              <div key={issue.id} className="flex items-start justify-between gap-4 py-4 first:pt-0 last:pb-0">
                <div className="flex items-start gap-3">
                  <Badge variant={issue.severity}>{issue.severity}</Badge>
                  <div>
                    <p className="text-sm font-medium text-slate-200">{issue.title}</p>
                    <p className="mt-0.5 text-sm text-slate-500">{issue.description}</p>
                    <p className="mt-1.5 text-xs text-slate-600">
                      {CATEGORY_LABELS[issue.category]} &middot; detected {new Date(issue.detectedAt).toLocaleString()}
                    </p>
                  </div>
                </div>
                <Link
                  to={`/endpoints/${issue.endpointId}`}
                  className="whitespace-nowrap rounded-lg border border-slate-700 px-3 py-1.5 text-xs font-medium text-slate-300 hover:border-indigo-500/50 hover:text-indigo-300"
                >
                  {issue.hostname}
                </Link>
              </div>
            ))}
          </div>
        )}
      </Card>
    </div>
  );
}
