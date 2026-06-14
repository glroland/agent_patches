import { Link, useOutletContext } from 'react-router-dom';
import StatCard from '../components/StatCard';
import Card from '../components/Card';
import Badge from '../components/Badge';
import TimelineEntry from '../components/TimelineEntry';
import { recentActivity, pendingApprovals, STATUS_META } from '../data/agents';
import { relativeTime } from '../utils/time';

export default function Dashboard() {
  const { agents } = useOutletContext();
  const attention = agents.filter((a) => a.status === 'attention' || a.status === 'offline');
  const approvals = pendingApprovals(agents);
  const activity = recentActivity(agents, 8);
  const openRecommendations = agents.flatMap((a) => a.timeline.filter((t) => t.type === 'recommendation')).length;

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold text-slate-100">Fleet Activity</h1>
        <p className="mt-1 text-sm text-slate-500">What your agents are seeing, doing, and asking for.</p>
      </div>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          label="Agents healthy"
          value={`${agents.length - attention.length} / ${agents.length}`}
          tone={attention.length === 0 ? 'success' : 'default'}
          hint="Active or idle with nothing flagged"
        />
        <StatCard
          label="Needs attention"
          value={attention.length}
          tone={attention.length > 0 ? 'warning' : 'success'}
          hint="Agents reporting an issue or unreachable"
        />
        <StatCard
          label="Approvals waiting on you"
          value={approvals.length}
          tone={approvals.length > 0 ? 'danger' : 'success'}
          hint={approvals.some((a) => a.risk === 'high') ? 'Includes high-risk requests' : 'Review when convenient'}
        />
        <StatCard
          label="Open recommendations"
          value={openRecommendations}
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
            {activity.map((entry) => (
              <TimelineEntry key={entry.id} entry={entry} showAgent />
            ))}
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
                      <Badge variant={agent.status}>{STATUS_META[agent.status].label}</Badge>
                    </div>
                    <p className="mt-1 text-xs text-slate-500">
                      {agent.currentTask ?? 'Last seen ' + relativeTime(agent.lastPoll)}
                    </p>
                  </Link>
                ))}
              </div>
            )}
          </Card>

          <Card
            title="Pending approvals"
            subtitle="Actions your agents want to take"
            action={<Link to="/approvals" className="text-xs font-medium text-indigo-400 hover:text-indigo-300">Review &rarr;</Link>}
          >
            {approvals.length === 0 ? (
              <p className="text-sm text-slate-500">Nothing waiting on you right now.</p>
            ) : (
              <div className="space-y-3">
                {approvals.slice(0, 4).map((req) => (
                  <Link key={req.id} to={`/agents/${req.agentId}`} className="block rounded-lg border border-slate-800 p-3 transition-colors hover:border-indigo-500/40">
                    <div className="flex items-center justify-between gap-2">
                      <p className="text-sm font-medium text-slate-100">{req.hostname}</p>
                      <Badge variant={req.risk}>{req.risk} risk</Badge>
                    </div>
                    <p className="mt-1 text-xs text-slate-500 line-clamp-2">{req.title}</p>
                  </Link>
                ))}
              </div>
            )}
          </Card>
        </div>
      </div>
    </div>
  );
}
