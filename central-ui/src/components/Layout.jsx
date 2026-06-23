import { NavLink, Outlet } from 'react-router-dom';
import { DashboardIcon, ServerIcon, AlertIcon, HandIcon, ChatIcon, LightbulbIcon, WrenchIcon } from './icons';
import { useFleetSocket } from '../hooks/useFleetSocket';
import logo from '../assets/logo.png';

const navItems = [
  { to: '/', label: 'Dashboard', icon: DashboardIcon, exact: true },
  { to: '/intelligence', label: 'Fleet Intelligence', icon: LightbulbIcon },
  { to: '/agents', label: 'Agents', icon: ServerIcon },
  { to: '/approvals', label: 'Approvals', icon: HandIcon },
  { to: '/issues', label: 'Issues & Concerns', icon: AlertIcon },
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
      ? 'text-rose-400 bg-rose-500/10 hover:bg-rose-500/20 hover:text-rose-300'
      : urgency === 'pending'
      ? 'text-amber-400 bg-amber-500/10 hover:bg-amber-500/20 hover:text-amber-300'
      : 'text-slate-400 hover:bg-slate-800/60 hover:text-slate-200';

  return (
    <div className="flex min-h-screen bg-slate-950">
      <aside className="flex w-64 flex-col border-r border-slate-800 bg-slate-900/40 px-4 py-6">
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
                      ? 'bg-indigo-500/15 text-indigo-300'
                      : isApprovals
                      ? approvalInactiveClass
                      : 'text-slate-400 hover:bg-slate-800/60 hover:text-slate-200'
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
                      ? 'bg-rose-500/20 text-rose-300'
                      : 'bg-amber-500/20 text-amber-300'
                  }`}>
                    {summary.pendingApprovalCount}
                  </span>
                ) : !!badge ? (
                  <span className="rounded-full bg-rose-500/20 px-2 py-0.5 text-xs font-semibold text-rose-300">
                    {badge}
                  </span>
                ) : null}
              </NavLink>
            );
          })}
        </nav>

        <div className="mt-auto rounded-lg border border-slate-800 bg-slate-900/60 p-3">
          {summary ? (
            <>
              <p className="text-xs font-medium text-slate-300">{summary.totalAgents} agents enrolled</p>
              <p className="mt-1 text-xs text-slate-500">
                {summary.totalAgents - summary.attentionCount} healthy &middot; {summary.attentionCount} need attention
              </p>
            </>
          ) : (
            <p className="text-xs text-slate-500">Loading fleet summary...</p>
          )}
        </div>
      </aside>

      <div className="flex flex-1 flex-col">
        <header className="flex items-center justify-between border-b border-slate-800 bg-slate-900/30 px-8 py-4">
          <div>
            <p className="text-xs text-slate-500">Inventory source</p>
            <p className="text-sm font-medium text-slate-200">central-backend</p>
          </div>
          <div className="flex items-center gap-3">
            <div className="flex items-center gap-1.5">
              <span
                className={`h-2 w-2 rounded-full ${connected ? 'bg-emerald-400' : 'bg-amber-400 animate-pulse'}`}
              />
              <span className="text-xs text-slate-500">{connected ? 'Live' : 'Reconnecting...'}</span>
            </div>
            <div className="h-9 w-9 rounded-full bg-gradient-to-br from-slate-700 to-slate-800 ring-1 ring-slate-700" />
          </div>
        </header>
        <main className="flex-1 px-8 py-6">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
