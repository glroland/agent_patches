import { NavLink, Outlet } from 'react-router-dom';
import { DashboardIcon, ServerIcon, AlertIcon, HandIcon, ChatIcon, LightbulbIcon, BoltIcon, WrenchIcon } from './icons';
import { useFleetSocket } from '../hooks/useFleetSocket';
import logo from '../assets/logo.png';

const navItems = [
  { to: '/', label: 'Dashboard', icon: DashboardIcon, exact: true },
  { to: '/issues', label: 'Issues & Concerns', icon: AlertIcon },
  { to: '/approvals', label: 'Approvals', icon: HandIcon },
  { to: '/agents', label: 'Agentic Fleet', icon: ServerIcon },
  { to: '/activity', label: 'Fleet Activity', icon: BoltIcon },
  { to: '/intelligence', label: 'Fleet Intelligence', icon: LightbulbIcon },
  { to: '/chat', label: 'Fleet Chat', icon: ChatIcon },
  { to: '/admin', label: 'Admin', icon: WrenchIcon },
];

const ONE_WEEK_MS = 7 * 24 * 60 * 60 * 1000;

// Returns 'old' | 'pending' | 'none' based on approval count and age.
function approvalUrgency(summary) {
  if (!summary || summary.pendingApprovalCount === 0) return 'none';
  if (summary.oldestPendingApprovalTime) {
    const age = Date.now() - new Date(summary.oldestPendingApprovalTime).getTime();
    if (age >= ONE_WEEK_MS) return 'old';
  }
  return 'pending';
}

export default function Layout() {
  const { summary, connected } = useFleetSocket();

  const badgeFor = summary
    ? {
        '/agents': summary.attentionCount,
        '/issues': summary.criticalIssueCount,
      }
    : {};

  const urgency = approvalUrgency(summary);

  // Inactive style for the Approvals link varies by urgency.
  const approvalInactiveClass =
    urgency === 'old'
      ? 'text-rose-700 bg-rose-50 hover:bg-rose-100 hover:text-rose-800'
      : urgency === 'pending'
      ? 'text-amber-700 bg-amber-50 hover:bg-amber-100 hover:text-amber-800'
      : 'text-navy-600 hover:bg-navy-50 hover:text-navy-900';

  return (
    <div className="flex h-screen overflow-hidden bg-white">
      <aside className="flex h-full w-64 flex-col overflow-y-auto border-r border-navy-200 bg-navy-50 px-4 py-6">
        <div className="mb-8 flex flex-col items-center px-2 text-center">
          <img src={logo} alt="Agent Patches" className="h-48 w-48 rounded-lg object-contain" />
        </div>

        <nav className="flex flex-col gap-1">
          {navItems.map(({ to, label, icon: Icon, exact }) => {
            const badge = badgeFor[to];
            const isApprovals = to === '/approvals';
            return (
              <NavLink
                key={to}
                to={to}
                end={exact}
                className={({ isActive }) =>
                  `flex items-center justify-between rounded-lg px-3 py-2.5 text-sm font-medium transition-colors ${
                    isActive
                      ? 'bg-navy-100 text-navy-700'
                      : isApprovals
                      ? approvalInactiveClass
                      : 'text-navy-600 hover:bg-navy-50 hover:text-navy-900'
                  }`
                }
              >
                <span className="flex items-center gap-3">
                  <Icon className="h-4.5 w-4.5" />
                  {label}
                </span>
                {isApprovals && summary?.pendingApprovalCount > 0 ? (
                  <span className={`rounded-full px-2 py-0.5 text-xs font-semibold ${
                    urgency === 'old'
                      ? 'bg-rose-100 text-rose-700'
                      : 'bg-amber-100 text-amber-700'
                  }`}>
                    {summary.pendingApprovalCount}
                  </span>
                ) : !!badge ? (
                  <span className="rounded-full bg-rose-100 px-2 py-0.5 text-xs font-semibold text-rose-700">
                    {badge}
                  </span>
                ) : null}
              </NavLink>
            );
          })}
        </nav>

        <div className="mt-auto rounded-lg border border-navy-200 bg-white p-3">
          {summary ? (
            <>
              <div className="flex items-center justify-between gap-2">
                <p className="text-xs font-medium text-navy-700">{summary.totalAgents} agents enrolled</p>
                <span className="flex shrink-0 items-center gap-1.5">
                  <span
                    className={`h-2 w-2 rounded-full ${connected ? 'bg-emerald-400' : 'bg-amber-400 animate-pulse'}`}
                  />
                  <span className="text-xs text-navy-500">{connected ? 'Live' : 'Reconnecting...'}</span>
                </span>
              </div>
              <p className="mt-1 text-xs text-navy-500">
                {summary.totalAgents - summary.attentionCount} healthy &middot; {summary.attentionCount} need attention
              </p>
            </>
          ) : (
            <p className="text-xs text-navy-500">Loading fleet summary...</p>
          )}
        </div>
      </aside>

      <main className="flex-1 overflow-y-auto px-8 py-6">
        <Outlet />
      </main>
    </div>
  );
}
