import { Link } from 'react-router-dom';
import Card from '../components/Card';
import Badge from '../components/Badge';
import TimelineEntry from '../components/TimelineEntry';
import AsyncState from '../components/AsyncState';
import { useFleetSocket } from '../hooks/useFleetSocket';
import { relativeTime } from '../utils/time';

export default function ActivityFeed() {
  const { dashboard, agents } = useFleetSocket();

  if (!dashboard) {
    return <AsyncState loading loadingLabel="Loading activity..." />;
  }

  const { activity, attention } = dashboard;

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold text-black">Activity</h1>
        <p className="mt-1 text-sm text-navy-500">Fleet status, recent observations, and patch history.</p>
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <Card title="Needs your attention" subtitle="Agents flagging a problem">
          {attention.length === 0 ? (
            <p className="text-sm text-navy-500">All agents are reporting normal status.</p>
          ) : (
            <div className="space-y-3">
              {attention.map((agent) => (
                <Link key={agent.id} to={`/agents/${agent.id}`} className="block rounded-lg border border-navy-200 p-3 transition-colors hover:border-amber-400">
                  <div className="flex items-center justify-between">
                    <p className="text-sm font-medium text-black">{agent.hostname}</p>
                    <Badge variant={agent.status}>{agent.statusLabel}</Badge>
                  </div>
                  <p className="mt-1 text-xs text-navy-500">
                    {agent.currentTask ?? 'Last seen ' + relativeTime(agent.lastPoll)}
                  </p>
                </Link>
              ))}
            </div>
          )}
        </Card>

        <Card title="Patch status" subtitle="Last time updates were applied per agent">
          {!agents || agents.length === 0 ? (
            <p className="text-sm text-navy-500">No agents enrolled.</p>
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
                      ? 'text-navy-500'
                      : age > 30 * 24 * 60 * 60 * 1000
                      ? 'text-rose-700'
                      : age > 7 * 24 * 60 * 60 * 1000
                      ? 'text-amber-700'
                      : 'text-emerald-700';
                  return (
                    <Link
                      key={agent.id}
                      to={`/agents/${agent.id}`}
                      className="flex items-center justify-between rounded-lg border border-navy-200 px-3 py-2 transition-colors hover:border-navy-300"
                    >
                      <p className="text-sm text-navy-900 truncate">{agent.displayName || agent.hostname}</p>
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

      <Card title="Live activity feed" subtitle="Recent observations, actions, and recommendations across the fleet">
        {activity.length === 0 ? (
          <p className="text-sm text-navy-500">No recent activity.</p>
        ) : (
          <div className="space-y-5">
            {activity.map((entry) => (
              <TimelineEntry key={entry.id} entry={entry} showAgent />
            ))}
          </div>
        )}
      </Card>
    </div>
  );
}
