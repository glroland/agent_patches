import { NavLink, Outlet } from 'react-router-dom';
import { DashboardIcon, ServerIcon, AlertIcon, HandIcon } from './icons';
import { agents, pendingApprovals, concerns } from '../data/agents';

const navItems = [
  { to: '/', label: 'Dashboard', icon: DashboardIcon, exact: true },
  { to: '/agents', label: 'Agents', icon: ServerIcon },
  { to: '/approvals', label: 'Approvals', icon: HandIcon },
  { to: '/issues', label: 'Issues & Concerns', icon: AlertIcon },
];

export default function Layout() {
  const attentionCount = agents.filter((a) => a.status === 'attention' || a.status === 'offline').length;
  const approvalCount = pendingApprovals().length;
  const criticalConcerns = concerns().filter((c) => c.severity === 'critical').length;

  const badgeFor = {
    '/agents': attentionCount,
    '/approvals': approvalCount,
    '/issues': criticalConcerns,
  };

  return (
    <div className="flex min-h-screen bg-slate-950">
      <aside className="flex w-64 flex-col border-r border-slate-800 bg-slate-900/40 px-4 py-6">
        <div className="mb-8 flex items-center gap-2.5 px-2">
          <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-gradient-to-br from-indigo-500 to-violet-600 text-sm font-bold text-white shadow-lg shadow-indigo-900/40">
            AP
          </div>
          <div>
            <p className="text-sm font-semibold text-slate-100">agent_patches</p>
            <p className="text-xs text-slate-500">Fleet Console</p>
          </div>
        </div>

        <nav className="flex flex-col gap-1">
          {navItems.map(({ to, label, icon: Icon, exact }) => {
            const badge = badgeFor[to];
            return (
              <NavLink
                key={to}
                to={to}
                end={exact}
                className={({ isActive }) =>
                  `flex items-center justify-between rounded-lg px-3 py-2.5 text-sm font-medium transition-colors ${
                    isActive
                      ? 'bg-indigo-500/15 text-indigo-300'
                      : 'text-slate-400 hover:bg-slate-800/60 hover:text-slate-200'
                  }`
                }
              >
                <span className="flex items-center gap-3">
                  <Icon className="h-4.5 w-4.5" />
                  {label}
                </span>
                {!!badge && (
                  <span className="rounded-full bg-rose-500/20 px-2 py-0.5 text-xs font-semibold text-rose-300">
                    {badge}
                  </span>
                )}
              </NavLink>
            );
          })}
        </nav>

        <div className="mt-auto rounded-lg border border-slate-800 bg-slate-900/60 p-3">
          <p className="text-xs font-medium text-slate-300">{agents.length} agents enrolled</p>
          <p className="mt-1 text-xs text-slate-500">
            {agents.length - attentionCount} healthy &middot; {attentionCount} need attention
          </p>
        </div>
      </aside>

      <div className="flex flex-1 flex-col">
        <header className="flex items-center justify-between border-b border-slate-800 bg-slate-900/30 px-8 py-4">
          <div>
            <p className="text-xs text-slate-500">Last polled</p>
            <p className="text-sm font-medium text-slate-200">just now (mock data)</p>
          </div>
          <div className="flex items-center gap-3">
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
