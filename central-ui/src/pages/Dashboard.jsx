import { Link } from 'react-router-dom';
import StatCard from '../components/StatCard';
import Card from '../components/Card';
import Badge from '../components/Badge';
import TimelineEntry from '../components/TimelineEntry';
import AsyncState from '../components/AsyncState';
import { useFleetSocket } from '../hooks/useFleetSocket';
import { relativeTime } from '../utils/time';

const PRIORITY_STYLES = {
  high:   { badge: 'bg-rose-500/15 text-rose-300',   border: 'border-rose-500/20'   },
  medium: { badge: 'bg-amber-500/15 text-amber-300',  border: 'border-amber-500/20'  },
  low:    { badge: 'bg-slate-700/60 text-slate-400',  border: 'border-slate-700/60'  },
};

const CATEGORY_LABEL = {
  health:        'Health',
  security:      'Security',
  feature:       'New Feature',
  configuration: 'Config',
};

function IntelligencePanel({ report }) {
  if (report === undefined) {
    // Feature not configured.
    return null;
  }

  if (report === null) {
    // Configured but first analysis not yet ready.
    return (
      <div className="rounded-xl border border-indigo-500/20 bg-indigo-500/5 px-5 py-4">
        <div className="flex items-center gap-3">
          <span className="h-2 w-2 animate-pulse rounded-full bg-indigo-400" />
          <p className="text-sm font-medium text-indigo-300">Fleet intelligence is analysing your environment…</p>
        </div>
      </div>
    );
  }

  const { headline, recommendations, generatedAt } = report;

  return (
    <div className="rounded-xl border border-indigo-500/25 bg-indigo-500/5 p-5 space-y-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <p className="text-xs font-semibold uppercase tracking-wider text-indigo-400 mb-1">Fleet Intelligence</p>
          <p className="text-base font-medium text-slate-100">{headline}</p>
        </div>
        <p className="shrink-0 text-xs text-slate-600 mt-0.5">{relativeTime(generatedAt)}</p>
      </div>

      {recommendations.length > 0 && (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
          {recommendations.map((rec, i) => {
            const styles = PRIORITY_STYLES[rec.priority] ?? PRIORITY_STYLES.low;
            return (
              <div key={i} className={`rounded-lg border bg-slate-900/60 p-3.5 ${styles.border}`}>
                <div className="flex items-center gap-2 mb-2">
                  <span className={`rounded-full px-2 py-0.5 text-xs font-semibold ${styles.badge}`}>
                    {rec.priority}
                  </span>
                  {rec.category && (
                    <span className="text-xs text-slate-500">
                      {CATEGORY_LABEL[rec.category] ?? rec.category}
                    </span>
                  )}
                </div>
                <p className="text-sm font-medium text-slate-200 mb-1">{rec.title}</p>
                <p className="text-xs text-slate-500 leading-relaxed">{rec.body}</p>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

export default function Dashboard() {
  const { dashboard, agents, intelligence } = useFleetSocket();

  if (!dashboard) {
    return <AsyncState loading loadingLabel="Loading fleet activity..." />;
  }

  const { stats, attention, activity } = dashboard;

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold text-slate-100">Fleet Activity</h1>
        <p className="mt-1 text-sm text-slate-500">What your agents are seeing, doing, and asking for.</p>
      </div>

      <IntelligencePanel report={intelligence} />

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

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <Card
          title="Live activity feed"
          subtitle="Most recent observations, actions, and recommendations across the fleet"
          className="lg:col-span-2"
        >
          <div className="space-y-5">
            {activity.length === 0 ? (
              <p className="text-sm text-slate-500">No recent activity.</p>
            ) : (
              activity.map((entry) => (
                <TimelineEntry key={entry.id} entry={entry} showAgent />
              ))
            )}
          </div>
        </Card>

        <div className="space-y-4">
          <Card title="Needs your attention" subtitle="Agents flagging a problem">
            {attention.length === 0 ? (
              <p className="text-sm text-slate-500">All agents are reporting normal status.</p>
            ) : (
              <div className="space-y-3">
                {attention.map((agent) => (
                  <Link key={agent.id} to={`/agents/${agent.id}`} className="block rounded-lg border border-slate-800 p-3 transition-colors hover:border-amber-500/40">
                    <div className="flex items-center justify-between">
                      <p className="text-sm font-medium text-slate-100">{agent.hostname}</p>
                      <Badge variant={agent.status}>{agent.statusLabel}</Badge>
                    </div>
                    <p className="mt-1 text-xs text-slate-500">
                      {agent.currentTask ?? 'Last seen ' + relativeTime(agent.lastPoll)}
                    </p>
                  </Link>
                ))}
              </div>
            )}
          </Card>

          <Card title="Patch status" subtitle="Last time updates were applied per agent">
            {!agents || agents.length === 0 ? (
              <p className="text-sm text-slate-500">No agents enrolled.</p>
            ) : (
              <div className="space-y-2">
                {[...agents]
                  .sort((a, b) => {
                    if (!a.lastPatchedAt && !b.lastPatchedAt) return 0;
                    if (!a.lastPatchedAt) return -1;
                    if (!b.lastPatchedAt) return 1;
                    return new Date(a.lastPatchedAt) - new Date(b.lastPatchedAt);
                  })
                  .map((agent) => {
                    const age = agent.lastPatchedAt
                      ? Date.now() - new Date(agent.lastPatchedAt).getTime()
                      : null;
                    const tone =
                      age === null
                        ? 'text-slate-500'
                        : age > 30 * 24 * 60 * 60 * 1000
                        ? 'text-rose-400'
                        : age > 7 * 24 * 60 * 60 * 1000
                        ? 'text-amber-400'
                        : 'text-emerald-400';
                    return (
                      <Link
                        key={agent.id}
                        to={`/agents/${agent.id}`}
                        className="flex items-center justify-between rounded-lg border border-slate-800 px-3 py-2 transition-colors hover:border-slate-700"
                      >
                        <p className="text-sm text-slate-200 truncate">{agent.displayName || agent.hostname}</p>
                        <p className={`shrink-0 pl-3 text-xs font-medium ${tone}`}>
                          {agent.lastPatchedAt ? relativeTime(agent.lastPatchedAt) : 'Never'}
                        </p>
                      </Link>
                    );
                  })}
              </div>
            )}
          </Card>
        </div>
      </div>
    </div>
  );
}
