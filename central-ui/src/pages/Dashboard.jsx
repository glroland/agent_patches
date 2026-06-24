import { Link } from 'react-router-dom';
import StatCard from '../components/StatCard';
import AsyncState from '../components/AsyncState';
import DashboardChat from '../components/DashboardChat';
import { useFleetSocket } from '../hooks/useFleetSocket';
import { relativeTime } from '../utils/time';

function IntelligenceBanner({ report }) {
  if (report === undefined) return null;

  if (report === null) {
    return (
      <div className="rounded-xl border border-indigo-500/20 bg-indigo-500/5 px-5 py-3.5">
        <div className="flex items-center gap-3">
          <span className="h-2 w-2 animate-pulse rounded-full bg-indigo-400" />
          <p className="text-sm font-medium text-indigo-300">Fleet intelligence is analysing your environment…</p>
        </div>
      </div>
    );
  }

  const { headline, recommendations, generatedAt } = report;
  const highCount = recommendations.filter((r) => r.priority === 'high').length;

  return (
    <div className="flex items-center justify-between gap-4 rounded-xl border border-indigo-500/25 bg-indigo-500/5 px-5 py-3.5">
      <div className="flex items-center gap-3 min-w-0">
        <span className="h-2 w-2 shrink-0 rounded-full bg-indigo-400" />
        <div className="min-w-0">
          <p className="text-xs font-semibold uppercase tracking-wider text-indigo-400 mb-0.5">Fleet Intelligence</p>
          <p className="text-sm font-medium text-slate-200 truncate">{headline}</p>
        </div>
      </div>
      <div className="shrink-0 flex items-center gap-4">
        {highCount > 0 && (
          <span className="text-xs font-semibold text-rose-300">{highCount} high-priority</span>
        )}
        <span className="text-xs text-slate-600">{relativeTime(generatedAt)}</span>
        <Link
          to="/intelligence"
          className="rounded-lg border border-indigo-500/30 px-3 py-1.5 text-xs font-medium text-indigo-300 hover:border-indigo-400/50 hover:text-indigo-200 transition-colors whitespace-nowrap"
        >
          See all →
        </Link>
      </div>
    </div>
  );
}

export default function Dashboard() {
  const { dashboard, intelligence } = useFleetSocket();

  if (!dashboard) {
    return <AsyncState loading loadingLabel="Loading fleet activity..." />;
  }

  const { stats } = dashboard;

  return (
    <div className="space-y-6">
      <IntelligenceBanner report={intelligence} />

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
