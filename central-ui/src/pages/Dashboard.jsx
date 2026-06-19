import { Link } from 'react-router-dom';
import StatCard from '../components/StatCard';
import Card from '../components/Card';
import Badge from '../components/Badge';
import TimelineEntry from '../components/TimelineEntry';
import AsyncState from '../components/AsyncState';
import { useFleetSocket } from '../hooks/useFleetSocket';
import { relativeTime } from '../utils/time';

export default function Dashboard() {
  const { dashboard } = useFleetSocket();

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
      </div>
    </div>
  );
}
