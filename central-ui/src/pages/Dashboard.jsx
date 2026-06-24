import { Link } from 'react-router-dom';
import StatCard from '../components/StatCard';
import AsyncState from '../components/AsyncState';
import DashboardChat from '../components/DashboardChat';
import { useFleetSocket } from '../hooks/useFleetSocket';
import { relativeTime } from '../utils/time';

const SEVERITY_DOT = {
  critical: 'bg-rose-400',
  warning: 'bg-amber-400',
  info: 'bg-sky-400',
};

const URGENCY_STYLES = {
  action: {
    border: 'border-rose-300',
    bg: 'bg-rose-50',
    dot: 'bg-rose-400',
    label: 'text-rose-700',
  },
  watch: {
    border: 'border-amber-300',
    bg: 'bg-amber-50',
    dot: 'bg-amber-400',
    label: 'text-amber-700',
  },
  calm: {
    border: 'border-emerald-200',
    bg: 'bg-emerald-50',
    dot: 'bg-emerald-400',
    label: 'text-emerald-700',
  },
};

function WelcomeBriefing({ briefing, intelligence }) {
  // Loading state — intelligence configured but neither ready yet
  if (briefing === null) {
    return (
      <div className="rounded-xl border border-navy-200 bg-navy-50 px-5 py-4">
        <div className="flex items-center gap-3">
          <span className="h-2 w-2 animate-pulse rounded-full bg-navy-400" />
          <p className="text-sm text-navy-600">Preparing your briefing…</p>
        </div>
      </div>
    );
  }

  // Intelligence not configured — fall back to slim intelligence banner or nothing
  if (briefing === undefined || briefing === null) {
    if (!intelligence) return null;
    const { headline, recommendations, generatedAt } = intelligence;
    const highCount = (recommendations || []).filter((r) => r.priority === 'high').length;
    return (
      <div className="flex items-center justify-between gap-4 rounded-xl border border-navy-200 bg-navy-50 px-5 py-3.5">
        <div className="flex items-center gap-3 min-w-0">
          <span className="h-2 w-2 shrink-0 rounded-full bg-navy-600" />
          <div className="min-w-0">
            <p className="text-xs font-semibold uppercase tracking-wider text-navy-600 mb-0.5">Fleet Intelligence</p>
            <p className="text-sm font-medium text-navy-900 truncate">{headline}</p>
          </div>
        </div>
        <div className="shrink-0 flex items-center gap-4">
          {highCount > 0 && <span className="text-xs font-semibold text-rose-700">{highCount} high-priority</span>}
          <span className="text-xs text-navy-400">{relativeTime(generatedAt)}</span>
          <Link to="/intelligence" className="rounded-lg border border-navy-300 px-3 py-1.5 text-xs font-medium text-navy-700 hover:border-navy-400 hover:text-navy-900 transition-colors whitespace-nowrap">
            See all →
          </Link>
        </div>
      </div>
    );
  }

  const { greeting, urgency, items, generatedAt } = briefing;
  const s = URGENCY_STYLES[urgency] ?? URGENCY_STYLES.calm;

  return (
    <div className={`rounded-xl border ${s.border} ${s.bg} px-5 py-4 space-y-3`}>
      <div className="flex items-start justify-between gap-4">
        <div className="flex items-center gap-3 min-w-0">
          <span className={`mt-0.5 h-2 w-2 shrink-0 rounded-full ${s.dot}`} />
          <p className="text-sm font-medium text-black">{greeting}</p>
        </div>
        <div className="shrink-0 flex items-center gap-3">
          <span className="text-xs text-navy-400">{relativeTime(generatedAt)}</span>
          {intelligence && (
            <Link to="/intelligence" className="rounded-lg border border-navy-300 px-2.5 py-1 text-xs font-medium text-navy-600 hover:border-navy-400 hover:text-navy-900 transition-colors whitespace-nowrap">
              Full report →
            </Link>
          )}
        </div>
      </div>

      {items && items.length > 0 && (
        <div className="ml-5 space-y-2">
          {items.map((item, i) => (
            <div key={i} className="flex gap-2.5">
              <span className={`mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full ${SEVERITY_DOT[item.severity] ?? 'bg-navy-400'}`} />
              <div>
                <p className="text-sm font-medium text-navy-900">{item.title}</p>
                {item.detail && <p className="text-xs text-navy-600 mt-0.5">{item.detail}</p>}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export default function Dashboard() {
  const { dashboard, intelligence, briefing } = useFleetSocket();

  if (!dashboard) {
    return <AsyncState loading loadingLabel="Loading fleet activity..." />;
  }

  const { stats } = dashboard;

  return (
    <div className="space-y-6">
      <WelcomeBriefing briefing={briefing} intelligence={intelligence} />

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          label="Agents healthy"
          value={`${stats.healthyAgents} / ${stats.totalAgents}`}
          tone={stats.attentionCount === 0 ? 'success' : 'default'}
          hint="Active or idle with nothing flagged"
        />
        <StatCard
          label="Needs attention"
          value={stats.attentionCount}
          tone={stats.attentionCount > 0 ? 'warning' : 'success'}
          hint="Agents reporting an issue or unreachable"
        />
        <StatCard
          label="Approvals waiting"
          value={stats.pendingApprovalCount}
          tone={stats.pendingApprovalCount > 0 ? 'danger' : 'success'}
          hint={stats.hasHighRiskApproval ? 'Includes high-risk requests' : 'Review when convenient'}
        />
        <StatCard
          label="Open recommendations"
          value={stats.openRecommendations}
          hint="Suggestions from agents, no action taken yet"
        />
      </div>

      <DashboardChat />
    </div>
  );
}
