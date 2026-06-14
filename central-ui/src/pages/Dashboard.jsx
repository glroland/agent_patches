import { Link } from 'react-router-dom';
import { PieChart, Pie, Cell, ResponsiveContainer, Tooltip, BarChart, Bar, XAxis, YAxis, CartesianGrid } from 'recharts';
import StatCard from '../components/StatCard';
import Card from '../components/Card';
import Badge from '../components/Badge';
import { endpoints, usedPct, formatBytes } from '../data/endpoints';
import { computeIssues } from '../data/issues';

const STATUS_COLORS = { online: '#34d399', degraded: '#fbbf24', offline: '#fb7185' };

export default function Dashboard() {
  const issues = computeIssues();
  const critical = issues.filter((i) => i.severity === 'critical').length;
  const warning = issues.filter((i) => i.severity === 'warning').length;

  const totalSecurityPatches = endpoints.reduce((sum, e) => sum + e.patches.security, 0);
  const totalPatches = endpoints.reduce((sum, e) => sum + e.patches.total, 0);

  const statusData = ['online', 'degraded', 'offline'].map((status) => ({
    name: status,
    value: endpoints.filter((e) => e.status === status).length,
  })).filter((d) => d.value > 0);

  const diskData = endpoints.map((e) => ({
    name: e.hostname.split('.')[0],
    usedPct: Math.max(...e.disks.map(usedPct)),
  }));

  const recentIssues = issues.slice(0, 6);

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold text-slate-100">Fleet Overview</h1>
        <p className="mt-1 text-sm text-slate-500">Status snapshot across all enrolled endpoint agents.</p>
      </div>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard label="Endpoints" value={endpoints.length} hint={`${endpoints.filter((e) => e.status === 'online').length} online`} />
        <StatCard
          label="Critical Issues"
          value={critical}
          tone={critical > 0 ? 'danger' : 'success'}
          hint={`${warning} warnings`}
        />
        <StatCard
          label="Security Patches Pending"
          value={totalSecurityPatches}
          tone={totalSecurityPatches > 0 ? 'warning' : 'success'}
          hint={`${totalPatches} total updates available`}
        />
        <StatCard
          label="SMART Failures"
          value={endpoints.reduce((sum, e) => sum + e.disks.filter((d) => d.smart?.status === 'FAILED').length, 0)}
          tone="danger"
          hint="Physical disks reporting failure"
        />
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <Card title="Fleet status" className="lg:col-span-1">
          <div className="h-48">
            <ResponsiveContainer width="100%" height="100%">
              <PieChart>
                <Pie data={statusData} dataKey="value" nameKey="name" innerRadius={45} outerRadius={70} paddingAngle={3}>
                  {statusData.map((entry) => (
                    <Cell key={entry.name} fill={STATUS_COLORS[entry.name]} stroke="none" />
                  ))}
                </Pie>
                <Tooltip
                  contentStyle={{ background: '#0f172a', border: '1px solid #1e293b', borderRadius: 8, fontSize: 12 }}
                  itemStyle={{ color: '#e2e8f0' }}
                />
              </PieChart>
            </ResponsiveContainer>
          </div>
          <div className="mt-2 flex justify-center gap-4">
            {statusData.map((d) => (
              <div key={d.name} className="flex items-center gap-1.5 text-xs text-slate-400">
                <span className="h-2.5 w-2.5 rounded-full" style={{ backgroundColor: STATUS_COLORS[d.name] }} />
                {d.name} ({d.value})
              </div>
            ))}
          </div>
        </Card>

        <Card title="Peak disk usage by host" subtitle="Highest mount utilization per endpoint" className="lg:col-span-2">
          <div className="h-56">
            <ResponsiveContainer width="100%" height="100%">
              <BarChart data={diskData} margin={{ left: -20 }}>
                <CartesianGrid strokeDasharray="3 3" stroke="#1e293b" vertical={false} />
                <XAxis dataKey="name" stroke="#64748b" fontSize={12} tickLine={false} axisLine={false} />
                <YAxis stroke="#64748b" fontSize={12} tickLine={false} axisLine={false} unit="%" domain={[0, 100]} />
                <Tooltip
                  contentStyle={{ background: '#0f172a', border: '1px solid #1e293b', borderRadius: 8, fontSize: 12 }}
                  itemStyle={{ color: '#e2e8f0' }}
                  formatter={(v) => [`${v.toFixed(1)}%`, 'Used']}
                />
                <Bar dataKey="usedPct" radius={[4, 4, 0, 0]}>
                  {diskData.map((d) => (
                    <Cell key={d.name} fill={d.usedPct >= 90 ? '#fb7185' : d.usedPct >= 75 ? '#fbbf24' : '#6366f1'} />
                  ))}
                </Bar>
              </BarChart>
            </ResponsiveContainer>
          </div>
        </Card>
      </div>

      <Card
        title="Recent issues"
        subtitle="Highest-severity findings across the fleet"
        action={<Link to="/issues" className="text-xs font-medium text-indigo-400 hover:text-indigo-300">View all &rarr;</Link>}
      >
        <div className="divide-y divide-slate-800">
          {recentIssues.map((issue) => (
            <div key={issue.id} className="flex items-center justify-between gap-4 py-3 first:pt-0 last:pb-0">
              <div className="flex items-center gap-3">
                <Badge variant={issue.severity}>{issue.severity}</Badge>
                <div>
                  <p className="text-sm font-medium text-slate-200">{issue.title}</p>
                  <p className="text-xs text-slate-500">{issue.description}</p>
                </div>
              </div>
              <Link to={`/endpoints/${issue.endpointId}`} className="whitespace-nowrap text-xs font-medium text-indigo-400 hover:text-indigo-300">
                {issue.hostname}
              </Link>
            </div>
          ))}
        </div>
      </Card>

      <Card title="Endpoints" subtitle="Quick-glance fleet table" action={<Link to="/endpoints" className="text-xs font-medium text-indigo-400 hover:text-indigo-300">Manage &rarr;</Link>}>
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="text-xs uppercase tracking-wide text-slate-500">
                <th className="pb-2 pr-4">Host</th>
                <th className="pb-2 pr-4">Status</th>
                <th className="pb-2 pr-4">OS</th>
                <th className="pb-2 pr-4">Disk</th>
                <th className="pb-2 pr-4">Memory</th>
                <th className="pb-2 pr-4">Patches</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800">
              {endpoints.map((e) => {
                const maxDisk = Math.max(...e.disks.map(usedPct));
                return (
                  <tr key={e.id} className="text-slate-300">
                    <td className="py-3 pr-4">
                      <Link to={`/endpoints/${e.id}`} className="font-medium text-slate-100 hover:text-indigo-300">{e.hostname}</Link>
                      <p className="text-xs text-slate-500">{e.ip}</p>
                    </td>
                    <td className="py-3 pr-4"><Badge variant={e.status}>{e.status}</Badge></td>
                    <td className="py-3 pr-4">{e.os} {e.osVersion}</td>
                    <td className="py-3 pr-4">{maxDisk.toFixed(0)}%</td>
                    <td className="py-3 pr-4">{formatBytes(e.memory.used)} / {formatBytes(e.memory.total)}</td>
                    <td className="py-3 pr-4">
                      {e.patches.total === 0 ? (
                        <span className="text-slate-500">Up to date</span>
                      ) : (
                        <span>{e.patches.total} ({e.patches.security} sec)</span>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </Card>
    </div>
  );
}
