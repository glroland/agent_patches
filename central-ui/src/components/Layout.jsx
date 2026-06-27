import { NavLink, Outlet } from 'react-router-dom';
import { DashboardIcon, ServerIcon, AlertIcon, HandIcon, ChatIcon, LightbulbIcon, BoltIcon, WrenchIcon, ChartIcon } from './icons';
import { useFleetSocket } from '../hooks/useFleetSocket';
import logo from '../assets/logo.png';

const navItems = [
  { to: '/', label: 'Dashboard', shortLabel: 'Dashboard', icon: DashboardIcon, exact: true },
  { to: '/intelligence', label: 'Fleet Intelligence', shortLabel: 'Intel', icon: LightbulbIcon },
  { to: '/approvals', label: 'Approvals', shortLabel: 'Approvals', icon: HandIcon },
  { to: '/agents', label: 'Agentic Fleet', shortLabel: 'Fleet', icon: ServerIcon },
  { to: '/activity', label: 'Fleet Activity', shortLabel: 'Activity', icon: BoltIcon },
  { to: '/issues', label: 'Issues & Concerns', shortLabel: 'Issues', icon: AlertIcon },
  { to: '/chat', label: 'Fleet Chat', shortLabel: 'Chat', icon: ChatIcon },
  { to: '/statistics', label: 'Statistics', shortLabel: 'Stats', icon: ChartIcon },
  { to: '/admin', label: 'Admin', shortLabel: 'Admin', icon: WrenchIcon },
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
      <aside className="flex h-full w-[94px] flex-col overflow-y-auto border-r border-navy-200 bg-navy-50 px-2 pb-4 pt-1">
        <div className="-mx-2 mb-4 flex flex-col items-center">
          <img src={logo} alt="Agent Patches" className="h-[100px] w-[100px] rounded-lg object-contain" />
        </div>

        <nav className="flex flex-col gap-1">
          {navItems.map(({ to, label, shortLabel, icon: Icon, exact }) => {
            const badge = badgeFor[to];
            const isApprovals = to === '/approvals';
            const dotColor = isApprovals && urgency === 'old' ? 'bg-rose-500' : 'bg-amber-500';
            return (
              <NavLink
                key={to}
                to={to}
                end={exact}
                title={label}
                className={({ isActive }) =>
                  `flex flex-col items-center gap-1 rounded-lg px-1 py-2 text-center transition-colors ${
                    isActive
                      ? 'bg-navy-100 text-navy-700'
                      : isApprovals
                      ? approvalInactiveClass
                      : 'text-navy-600 hover:bg-navy-50 hover:text-navy-900'
                  }`
                }
              >
                <span className="relative flex h-5 w-5 items-center justify-center">
                  <Icon className="h-5 w-5" />
                  {isApprovals && summary?.pendingApprovalCount > 0 ? (
                    <span className={`absolute -right-2 -top-2 flex h-3.5 min-w-3.5 items-center justify-center rounded-full px-1 text-[9px] font-bold text-white ${dotColor}`}>
                      {summary.pendingApprovalCount}
                    </span>
                  ) : !!badge ? (
                    <span className="absolute -right-2 -top-2 flex h-3.5 min-w-3.5 items-center justify-center rounded-full bg-rose-500 px-1 text-[9px] font-bold text-white">
                      {badge}
                    </span>
                  ) : null}
                </span>
                <span className="text-[11px] font-medium leading-tight">{shortLabel}</span>
              </NavLink>
            );
          })}
        </nav>

        <div className="mt-auto rounded-lg border border-navy-200 bg-white p-2 text-center">
          {summary ? (
            <>
              <p className="text-base font-semibold text-navy-900">{summary.totalAgents}</p>
              <p className="text-[9px] uppercase tracking-wide text-navy-500">agents</p>
              {summary.attentionCount > 0 && (
                <p className="mt-1 text-[10px] font-medium text-amber-700">{summary.attentionCount} need attn</p>
              )}
              <div className="mt-1.5 flex items-center justify-center gap-1">
                <span
                  className={`h-1.5 w-1.5 rounded-full ${connected ? 'bg-emerald-400' : 'bg-amber-400 animate-pulse'}`}
                />
                <span className="text-[9px] text-navy-500">{connected ? 'Live' : 'Offline'}</span>
              </div>
            </>
          ) : (
            <p className="text-[9px] text-navy-500">Loading...</p>
          )}
        </div>
      </aside>

      <main className="flex-1 overflow-y-auto px-8 py-6">
        <Outlet />
      </main>
    </div>
  );
}
