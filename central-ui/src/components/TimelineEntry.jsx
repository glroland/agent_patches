import { Link } from 'react-router-dom';
import Badge from './Badge';
import { EyeIcon, BoltIcon, LightbulbIcon, HandIcon } from './icons';
import { relativeTime } from '../utils/time';

const TYPE_META = {
  observation: { label: 'Observed', icon: EyeIcon, color: 'text-sky-400 bg-sky-500/10' },
  action: { label: 'Did', icon: BoltIcon, color: 'text-violet-400 bg-violet-500/10' },
  recommendation: { label: 'Recommends', icon: LightbulbIcon, color: 'text-amber-400 bg-amber-500/10' },
  approval: { label: 'Needs approval', icon: HandIcon, color: 'text-rose-400 bg-rose-500/10' },
};

export default function TimelineEntry({ entry, showAgent = false }) {
  const meta = TYPE_META[entry.type] ?? TYPE_META.observation;
  const Icon = meta.icon;

  return (
    <div className="flex gap-3">
      <div className={`mt-0.5 flex h-8 w-8 flex-none items-center justify-center rounded-full ${meta.color}`}>
        <Icon className="h-4 w-4" />
      </div>
      <div className="min-w-0 flex-1 pb-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-xs font-medium uppercase tracking-wide text-slate-500">{meta.label}</span>
          {entry.severity && <Badge variant={entry.severity}>{entry.severity}</Badge>}
          {entry.type === 'approval' && <Badge variant={entry.risk}>{entry.risk} risk</Badge>}
          {entry.type === 'approval' && entry.status !== 'pending' && <Badge variant={entry.status}>{entry.status}</Badge>}
          <span className="text-xs text-slate-600">{relativeTime(entry.time)}</span>
          {showAgent && (
            <Link to={`/agents/${entry.agentId}`} className="text-xs font-medium text-indigo-400 hover:text-indigo-300">
              {entry.hostname}
            </Link>
          )}
        </div>
        <p className="mt-1 text-sm font-medium text-slate-200">{entry.title}</p>
        <p className="mt-0.5 text-sm text-slate-500">{entry.detail}</p>
        {entry.type === 'approval' && entry.proposedAction && (
          <p className="mt-2 rounded-md bg-slate-800/70 px-3 py-2 font-mono text-xs text-slate-400">
            {entry.proposedAction}
          </p>
        )}
      </div>
    </div>
  );
}
