import { endpoints, usedPct, memUsedPct } from './endpoints';

// Derives a fleet-wide "issues & concerns" feed from per-endpoint state.
// In a real deployment this would come from the central-backend, which
// aggregates findings reported by each endpoint-server's skills.
export function computeIssues() {
  const issues = [];
  let nextId = 1;

  const push = (severity, category, endpoint, title, description, detectedAt) => {
    issues.push({
      id: nextId++,
      severity,
      category,
      endpointId: endpoint.id,
      hostname: endpoint.hostname,
      title,
      description,
      detectedAt: detectedAt ?? endpoint.lastCheckIn,
    });
  };

  for (const ep of endpoints) {
    if (ep.status === 'offline') {
      push('critical', 'connectivity', ep, 'Agent unreachable',
        ep.statusNote || 'The endpoint agent has not checked in recently.', ep.lastCheckIn);
    } else if (ep.status === 'degraded') {
      push('warning', 'connectivity', ep, 'Endpoint degraded',
        ep.statusNote || 'The endpoint reported a degraded status.', ep.lastCheckIn);
    }

    for (const disk of ep.disks) {
      const pct = usedPct(disk);
      if (pct >= 95) {
        push('critical', 'disk', ep, `Disk nearly full: ${disk.mount}`,
          `${disk.mount} (${disk.device}) is at ${pct.toFixed(1)}% capacity.`);
      } else if (pct >= 85) {
        push('warning', 'disk', ep, `Disk usage high: ${disk.mount}`,
          `${disk.mount} (${disk.device}) is at ${pct.toFixed(1)}% capacity.`);
      }

      if (disk.smart?.status === 'FAILED') {
        push('critical', 'hardware', ep, `S.M.A.R.T. failure: ${disk.smart.device}`,
          disk.smart.findings.join('; '));
      }
    }

    if (ep.patches.security > 0) {
      const sev = ep.patches.packages.some((p) => p.severity === 'critical') ? 'critical' : 'warning';
      const cves = ep.patches.packages.flatMap((p) => p.cve).filter(Boolean);
      push(sev, 'patching', ep, `${ep.patches.security} security update${ep.patches.security > 1 ? 's' : ''} pending`,
        `${ep.patches.summary}${cves.length ? ` — ${cves.join(', ')}` : ''}`);
    }

    const memPct = memUsedPct(ep.memory);
    if (memPct >= 90) {
      push('warning', 'memory', ep, 'Memory utilization high',
        `Memory usage at ${memPct.toFixed(1)}% of ${(ep.memory.total / 1024 ** 3).toFixed(0)} GB.`);
    }

    for (const session of ep.logins) {
      if (session.user === 'root' || session.user === 'Administrator') {
        push('warning', 'access', ep, `Privileged interactive session: ${session.user}`,
          `${session.user} logged in via ${session.tty} from ${session.remoteHost}.`, session.since);
      }
    }
  }

  const severityOrder = { critical: 0, warning: 1, info: 2 };
  return issues.sort((a, b) => severityOrder[a.severity] - severityOrder[b.severity]);
}
