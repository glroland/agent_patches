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

// Inline [CRITICAL] and [HIGH] markers with red text; leave everything else as-is.
function InlineWithSeverity({ text }) {
  const parts = text.split(/(\[(?:CRITICAL|HIGH)\])/);
  return parts.map((part, i) =>
    part === '[CRITICAL]' || part === '[HIGH]'
      ? <span key={i} className="font-semibold text-red-400">{part}</span>
      : <span key={i}>{part}</span>
  );
}

// Renders the detail field. When the text uses the bullet-summary format
// (lines starting with •) each bullet becomes its own row; CVE severity
// markers are coloured red. Plain text falls back to a simple paragraph.
function DetailText({ detail }) {
  if (!detail) return null;

  const lines = detail.split('\n');
  const hasBullets = lines.some((l) => l.startsWith('•'));

  if (!hasBullets) {
    return <p className="mt-0.5 text-sm text-slate-500">{detail}</p>;
  }

  return (
    <div className="mt-1 space-y-0.5 text-sm text-slate-500">
      {lines.map((line, i) => {
        if (!line) return null;

        if (line.startsWith('• ')) {
          return (
            <div key={i} className="flex gap-1.5">
              <span className="mt-px shrink-0 text-slate-600">•</span>
              <span><InlineWithSeverity text={line.slice(2)} /></span>
            </div>
          );
        }

        if (line.startsWith('  [')) {
          // CVE sub-lines indented under their bullet.
          return (
            <div key={i} className="pl-5 text-xs">
              <InlineWithSeverity text={line.trimStart()} />
            </div>
          );
        }

        // Header line or dist-upgrade notice.
        return (
          <p key={i} className={i === 0 ? 'font-medium text-slate-400' : ''}>
            <InlineWithSeverity text={line} />
          </p>
        );
      })}
    </div>
  );
}

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
        <DetailText detail={entry.detail} />
        {entry.type === 'approval' && entry.proposedAction && (
          <p className="mt-2 rounded-md bg-slate-800/70 px-3 py-2 font-mono text-xs text-slate-400">
            {entry.proposedAction}
          </p>
        )}
      </div>
    </div>
  );
}
