// Mock agent activity. Each endpoint is managed by its own AI agent that
// polls the host, makes observations, takes routine actions on its own, and
// surfaces recommendations or approval requests for a human operator. This
// models that activity stream — the manager cares about what each agent is
// seeing, doing, and asking for, not dashboards of percentages.
//
// Keyed by the short hostname (the first label of the FQDN) so it can be
// merged with inventory rows in services/fleet.js. Once agents report real
// activity, this file goes away.

export const STATUS_META = {
  active: { label: 'Active', description: 'Currently working on a task' },
  idle: { label: 'Idle', description: 'Healthy, nothing pending' },
  attention: { label: 'Needs attention', description: 'Flagged something for review' },
  offline: { label: 'Offline', description: 'Not responding to polls' },
};

export const mockActivityByHost = {
  web01: {
    os: 'Ubuntu 24.04 LTS',
    role: 'Frontend web server',
    tags: ['production', 'frontend'],
    status: 'active',
    lastPoll: '2026-06-13T20:42:11-04:00',
    currentTask: 'Reviewing nightly log growth and a pending OpenSSL security update',
    timeline: [
      {
        id: 'web01-4',
        time: '2026-06-13T20:30:00-04:00',
        type: 'approval',
        title: 'Requesting approval to apply 3 pending updates, including a critical OpenSSL fix',
        detail: 'CVE-2026-1010 affects the installed openssl package. Two other low-risk packages (libxml2, curl) can be updated in the same pass.',
        risk: 'high',
        proposedAction: 'apt-get upgrade openssl libxml2 curl — no service restart or reboot expected',
        status: 'pending',
      },
      {
        id: 'web01-3',
        time: '2026-06-13T20:18:00-04:00',
        type: 'recommendation',
        title: 'Suggest enabling daily log rotation with compression for the app access log',
        detail: 'access.log grew to 6.4 GB overnight without rotating. Compressing and rotating daily would keep this under control automatically.',
        severity: 'info',
      },
      {
        id: 'web01-2',
        time: '2026-06-13T20:12:00-04:00',
        type: 'action',
        title: 'Ran a disk scan to confirm what is driving disk growth',
        detail: 'Confirmed the root filesystem (/) is the only contributor — no NFS mounts or pseudo-filesystems involved. Largest growth is in /var/log/app.',
      },
      {
        id: 'web01-1',
        time: '2026-06-13T20:05:00-04:00',
        type: 'observation',
        title: 'Noticed unusually fast disk growth on the root filesystem overnight',
        detail: 'The app access log grew significantly faster than its weekly average. Investigating before it becomes a capacity concern.',
        severity: 'info',
      },
    ],
  },
  web02: {
    os: 'Ubuntu 24.04 LTS',
    role: 'Frontend web server',
    tags: ['production', 'frontend'],
    status: 'active',
    lastPoll: '2026-06-13T20:42:09-04:00',
    currentTask: 'Awaiting approval before applying the same OpenSSL security update as web01',
    timeline: [
      {
        id: 'web02-3',
        time: '2026-06-13T20:31:00-04:00',
        type: 'approval',
        title: 'Requesting approval to apply the OpenSSL security update (CVE-2026-1010)',
        detail: 'Same fix already requested for web01. Applying to both frontends keeps them in sync.',
        risk: 'high',
        proposedAction: 'apt-get upgrade openssl — no service restart or reboot expected',
        status: 'pending',
      },
      {
        id: 'web02-2',
        time: '2026-06-13T20:05:00-04:00',
        type: 'action',
        title: 'Completed the nightly S.M.A.R.T. health check',
        detail: 'All drives reported healthy with no findings of concern.',
      },
      {
        id: 'web02-1',
        time: '2026-06-13T20:00:00-04:00',
        type: 'observation',
        title: 'Everything looks normal',
        detail: 'No unusual disk growth, memory pressure, or login activity since the last poll.',
      },
    ],
  },
  db01: {
    os: 'Ubuntu 22.04 LTS',
    role: 'Primary database',
    tags: ['production', 'database'],
    status: 'attention',
    lastPoll: '2026-06-13T20:41:58-04:00',
    currentTask: 'Monitoring a failing disk on the /data volume and preparing a precautionary backup',
    timeline: [
      {
        id: 'db01-5',
        time: '2026-06-13T20:35:00-04:00',
        type: 'approval',
        title: 'Requesting approval to start a precautionary base backup to the replica’s spare volume',
        detail: 'Given the failing disk, a fresh backup off-host reduces risk while a replacement is arranged. This will run in the background but uses meaningful network and I/O.',
        risk: 'medium',
        proposedAction: 'pg_basebackup streamed to db02-replica:/data/backups/precautionary — estimated 90 minutes',
        status: 'pending',
      },
      {
        id: 'db01-4',
        time: '2026-06-13T20:20:00-04:00',
        type: 'recommendation',
        title: 'Recommend scheduling a disk replacement for /dev/nvme0n1',
        detail: 'Reallocated and pending sector counts are climbing and endurance is at 94%. This drive should be replaced before it fails outright.',
        severity: 'critical',
      },
      {
        id: 'db01-3',
        time: '2026-06-13T20:18:00-04:00',
        type: 'action',
        title: 'Sent a notification to the on-call channel about the failing disk',
        detail: 'Notified per the disk-space-check responsibility configuration.',
      },
      {
        id: 'db01-2',
        time: '2026-06-13T20:15:00-04:00',
        type: 'observation',
        title: 'S.M.A.R.T. health check failed on /dev/nvme0n1 (backing the /data volume)',
        detail: 'Overall health check reports FAILED, with 12 media errors and rising reallocated sector counts. Temperature is also elevated at 61°C.',
        severity: 'critical',
      },
      {
        id: 'db01-1',
        time: '2026-06-13T20:15:00-04:00',
        type: 'observation',
        title: 'A root session was opened from an external address',
        detail: 'A root login arrived from 203.0.113.44, outside the usual internal ranges. Worth confirming this was expected.',
        severity: 'warning',
      },
    ],
  },
  'db02-replica': {
    os: 'Ubuntu 22.04 LTS',
    role: 'Database replica',
    tags: ['production', 'database', 'replica'],
    status: 'attention',
    lastPoll: '2026-06-13T20:18:43-04:00',
    currentTask: 'Retrying replication connection to the primary',
    timeline: [
      {
        id: 'db02-3',
        time: '2026-06-13T20:18:00-04:00',
        type: 'recommendation',
        title: 'Recommend a human check the network path between this host and db01',
        detail: 'The agent will keep retrying automatically, but two consecutive reconnect attempts have failed.',
        severity: 'warning',
      },
      {
        id: 'db02-2',
        time: '2026-06-13T20:14:00-04:00',
        type: 'action',
        title: 'Attempted to reconnect to the primary — failed twice',
        detail: 'Connection attempts to db01 for streaming replication timed out after 10 seconds.',
      },
      {
        id: 'db02-1',
        time: '2026-06-13T20:00:00-04:00',
        type: 'observation',
        title: 'Replication lag is climbing',
        detail: 'Lag behind the primary has grown to roughly 5-6 minutes and is increasing each poll.',
        severity: 'warning',
      },
    ],
  },
  'win-dc01': {
    os: 'Windows Server 2022',
    role: 'Domain controller',
    tags: ['production', 'infrastructure'],
    status: 'active',
    lastPoll: '2026-06-13T20:40:02-04:00',
    currentTask: 'Awaiting approval to install a reboot-required cumulative update',
    timeline: [
      {
        id: 'win-dc01-3',
        time: '2026-06-13T20:25:00-04:00',
        type: 'approval',
        title: 'Requesting approval to install KB5041234 and reboot during the next maintenance window',
        detail: 'This cumulative update addresses two CVEs (CVE-2026-2200, CVE-2026-2201) and a .NET security update. Installing requires a reboot, which the agent would schedule for 02:00.',
        risk: 'high',
        proposedAction: 'Install KB5041234 + .NET security update, then reboot at 02:00 local time',
        status: 'pending',
      },
      {
        id: 'win-dc01-2',
        time: '2026-06-13T20:10:00-04:00',
        type: 'observation',
        title: 'An Administrator RDP session has been idle for over 90 minutes',
        detail: 'A privileged session from 10.0.0.9 has been open and idle since 18:55. Worth confirming it should remain open.',
        severity: 'warning',
      },
      {
        id: 'win-dc01-1',
        time: '2026-06-13T20:00:00-04:00',
        type: 'observation',
        title: 'Found 9 pending updates, 4 of them security-related',
        detail: 'Routine update check found new patches including the cumulative update above.',
      },
    ],
  },
  'edge-mac-mini': {
    os: 'macOS 15.4 Sequoia',
    role: 'Edge lab device',
    tags: ['lab', 'edge'],
    status: 'idle',
    lastPoll: '2026-06-13T20:41:30-04:00',
    currentTask: null,
    timeline: [
      {
        id: 'edge-2',
        time: '2026-06-13T20:00:00-04:00',
        type: 'action',
        title: 'Completed routine inventory and health scan',
        detail: 'Disk, memory, and network all within normal ranges. No updates pending.',
      },
      {
        id: 'edge-1',
        time: '2026-06-13T08:00:00-04:00',
        type: 'observation',
        title: 'Nothing notable since the last check-in',
        detail: 'No changes worth flagging.',
      },
    ],
  },
  build01: {
    os: 'Fedora 40',
    role: 'CI build runner',
    tags: ['ci'],
    status: 'offline',
    lastPoll: '2026-06-13T14:02:51-04:00',
    currentTask: null,
    timeline: [
      {
        id: 'build01-3',
        time: '2026-06-13T14:02:51-04:00',
        type: 'observation',
        title: 'Agent has not responded to polling since 14:02',
        detail: 'Connection refused on the last 6 poll attempts. Central will keep retrying; no further activity from this agent until it reconnects.',
        severity: 'critical',
      },
      {
        id: 'build01-2',
        time: '2026-06-13T06:05:00-04:00',
        type: 'recommendation',
        title: 'Recommend pruning unused container images',
        detail: '/var/lib/containers is using 320 GB, the largest contributor on this host by a wide margin.',
        severity: 'warning',
      },
      {
        id: 'build01-1',
        time: '2026-06-13T06:00:00-04:00',
        type: 'observation',
        title: 'Found 22 pending updates, 2 security-related (including the kernel)',
        detail: 'Kernel update addresses CVE-2026-1875. Will request approval once the agent is reachable again.',
      },
    ],
  },
};

// Fallback activity for agents present in the inventory but without mock
// activity data of their own.
export const defaultActivity = () => ({
  os: null,
  role: 'Endpoint agent',
  tags: [],
  status: 'idle',
  lastPoll: null,
  currentTask: null,
  timeline: [],
});
