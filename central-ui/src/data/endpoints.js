// Mock fleet data. Shapes loosely mirror the output of the endpoint-server
// skills (check_drives, check_for_pending_system_patches,
// check_interactive_logins, analyze_memory_utilization,
// analyze_network_utilization, capture_system_info).

const GB = 1024 ** 3;
const TB = 1024 ** 4;

export const endpoints = [
  {
    id: 'web01',
    hostname: 'web01.prod.internal',
    ip: '10.0.4.11',
    os: 'Ubuntu',
    osVersion: '24.04 LTS',
    agentVersion: '0.7.2',
    status: 'online',
    lastCheckIn: '2026-06-13T20:42:11-04:00',
    uptimeDays: 142,
    tags: ['production', 'frontend'],
    disks: [
      {
        mount: '/',
        device: '/dev/mapper/ubuntu--vg-root',
        fsType: 'ext4',
        total: 200 * GB,
        free: 9 * GB,
        smart: { status: 'PASSED', device: '/dev/sda', findings: ['No issues detected'] },
        topDirs: [
          { path: '/var/log', size: 41 * GB },
          { path: '/opt/app/releases', size: 38 * GB },
          { path: '/var/lib/docker', size: 22 * GB },
        ],
        topFiles: [
          { path: '/var/log/app/access.log', size: 6.4 * GB },
          { path: '/var/log/journal/system.journal', size: 1.8 * GB },
        ],
      },
    ],
    patches: {
      updatesAvailable: true,
      lastChecked: '2026-06-13T06:00:00-04:00',
      total: 14,
      security: 3,
      summary: '14 packages can be updated (3 security)',
      packages: [
        { name: 'openssl', current: '3.0.13-1', candidate: '3.0.15-1', severity: 'critical', cve: ['CVE-2026-1010'] },
        { name: 'libxml2', current: '2.9.14', candidate: '2.9.16', severity: 'high', cve: ['CVE-2026-0998'] },
        { name: 'curl', current: '8.5.0', candidate: '8.7.1', severity: 'medium', cve: [] },
      ],
    },
    logins: [
      { user: 'deploy', tty: 'pts/0', remoteHost: '10.0.1.50', since: '2026-06-13T19:02:00-04:00', idleMinutes: 4 },
    ],
    memory: { total: 16 * GB, used: 13.9 * GB, swapTotal: 4 * GB, swapUsed: 3.6 * GB },
    network: { rxMbps: 84.2, txMbps: 121.7, established: 312 },
  },
  {
    id: 'web02',
    hostname: 'web02.prod.internal',
    ip: '10.0.4.12',
    os: 'Ubuntu',
    osVersion: '24.04 LTS',
    agentVersion: '0.7.2',
    status: 'online',
    lastCheckIn: '2026-06-13T20:42:09-04:00',
    uptimeDays: 142,
    tags: ['production', 'frontend'],
    disks: [
      {
        mount: '/',
        device: '/dev/mapper/ubuntu--vg-root',
        fsType: 'ext4',
        total: 200 * GB,
        free: 96 * GB,
        smart: { status: 'PASSED', device: '/dev/sda', findings: ['No issues detected'] },
        topDirs: [
          { path: '/opt/app/releases', size: 38 * GB },
          { path: '/var/lib/docker', size: 22 * GB },
          { path: '/var/log', size: 9 * GB },
        ],
        topFiles: [{ path: '/var/log/app/access.log', size: 1.1 * GB }],
      },
    ],
    patches: {
      updatesAvailable: true,
      lastChecked: '2026-06-13T06:00:00-04:00',
      total: 6,
      security: 1,
      summary: '6 packages can be updated (1 security)',
      packages: [
        { name: 'openssl', current: '3.0.13-1', candidate: '3.0.15-1', severity: 'critical', cve: ['CVE-2026-1010'] },
      ],
    },
    logins: [],
    memory: { total: 16 * GB, used: 7.2 * GB, swapTotal: 4 * GB, swapUsed: 0 },
    network: { rxMbps: 79.5, txMbps: 118.3, established: 298 },
  },
  {
    id: 'db01',
    hostname: 'db01.prod.internal',
    ip: '10.0.4.21',
    os: 'Ubuntu',
    osVersion: '22.04 LTS',
    agentVersion: '0.7.1',
    status: 'online',
    lastCheckIn: '2026-06-13T20:41:58-04:00',
    uptimeDays: 365,
    tags: ['production', 'database'],
    disks: [
      {
        mount: '/',
        device: '/dev/sda1',
        fsType: 'ext4',
        total: 100 * GB,
        free: 61 * GB,
        smart: { status: 'PASSED', device: '/dev/sda', findings: ['No issues detected'] },
        topDirs: [
          { path: '/var/log', size: 4 * GB },
          { path: '/usr', size: 6 * GB },
        ],
        topFiles: [],
      },
      {
        mount: '/data',
        device: '/dev/nvme0n1p1',
        fsType: 'xfs',
        total: 2 * TB,
        free: 0.08 * TB,
        smart: {
          status: 'FAILED',
          device: '/dev/nvme0n1',
          findings: [
            'Overall SMART health check: FAILED',
            'NVMe media errors: 12',
            'NVMe endurance used: 94%',
            'Temperature: 61°C',
          ],
        },
        topDirs: [
          { path: '/data/pgsql/15/main/base', size: 1.6 * TB },
          { path: '/data/pgsql/15/main/pg_wal', size: 180 * GB },
          { path: '/data/backups', size: 90 * GB },
        ],
        topFiles: [
          { path: '/data/pgsql/15/main/base/16400/2619', size: 38 * GB },
        ],
      },
    ],
    patches: {
      updatesAvailable: false,
      lastChecked: '2026-06-13T06:00:00-04:00',
      total: 0,
      security: 0,
      summary: 'No updates available',
      packages: [],
    },
    logins: [
      { user: 'root', tty: 'pts/1', remoteHost: '203.0.113.44', since: '2026-06-13T20:15:00-04:00', idleMinutes: 0 },
    ],
    memory: { total: 64 * GB, used: 59.5 * GB, swapTotal: 8 * GB, swapUsed: 6.9 * GB },
    network: { rxMbps: 12.4, txMbps: 9.8, established: 64 },
  },
  {
    id: 'db02-replica',
    hostname: 'db02-replica.prod.internal',
    ip: '10.0.4.22',
    os: 'Ubuntu',
    osVersion: '22.04 LTS',
    agentVersion: '0.7.1',
    status: 'degraded',
    lastCheckIn: '2026-06-13T20:18:43-04:00',
    uptimeDays: 365,
    tags: ['production', 'database', 'replica'],
    disks: [
      {
        mount: '/',
        device: '/dev/sda1',
        fsType: 'ext4',
        total: 100 * GB,
        free: 70 * GB,
        smart: { status: 'PASSED', device: '/dev/sda', findings: ['No issues detected'] },
        topDirs: [{ path: '/var/log', size: 3 * GB }],
        topFiles: [],
      },
      {
        mount: '/data',
        device: '/dev/nvme0n1p1',
        fsType: 'xfs',
        total: 2 * TB,
        free: 0.4 * TB,
        smart: { status: 'PASSED', device: '/dev/nvme0n1', findings: ['No issues detected'] },
        topDirs: [{ path: '/data/pgsql/15/main/base', size: 1.5 * TB }],
        topFiles: [],
      },
    ],
    patches: {
      updatesAvailable: true,
      lastChecked: '2026-06-12T06:00:00-04:00',
      total: 2,
      security: 0,
      summary: '2 packages can be updated',
      packages: [{ name: 'vim', current: '9.0.1', candidate: '9.0.2', severity: 'low', cve: [] }],
    },
    logins: [],
    memory: { total: 64 * GB, used: 41 * GB, swapTotal: 8 * GB, swapUsed: 0 },
    network: { rxMbps: 3.1, txMbps: 2.7, established: 12 },
    statusNote: 'Replication lag 340s – last check-in 24 min ago',
  },
  {
    id: 'win-dc01',
    hostname: 'WIN-DC01',
    ip: '10.0.2.5',
    os: 'Windows Server',
    osVersion: '2022',
    agentVersion: '0.7.2',
    status: 'online',
    lastCheckIn: '2026-06-13T20:40:02-04:00',
    uptimeDays: 88,
    tags: ['production', 'infrastructure'],
    disks: [
      {
        mount: 'C:\\',
        device: '\\\\.\\PHYSICALDRIVE0',
        fsType: 'NTFS',
        total: 256 * GB,
        free: 18 * GB,
        smart: { status: 'unavailable', device: '', findings: [] },
        topDirs: [
          { path: 'C:\\Windows\\WinSxS', size: 14 * GB },
          { path: 'C:\\ProgramData\\Logs', size: 9 * GB },
        ],
        topFiles: [],
      },
    ],
    patches: {
      updatesAvailable: true,
      lastChecked: '2026-06-13T06:00:00-04:00',
      total: 9,
      security: 4,
      summary: '9 updates available (4 security, includes a reboot-required cumulative update)',
      packages: [
        { name: 'KB5041234 Cumulative Update', current: '-', candidate: '-', severity: 'critical', cve: ['CVE-2026-2200', 'CVE-2026-2201'] },
        { name: '.NET Framework Security Update', current: '-', candidate: '-', severity: 'high', cve: ['CVE-2026-2150'] },
      ],
    },
    logins: [
      { user: 'Administrator', tty: 'RDP-Tcp#3', remoteHost: '10.0.0.9', since: '2026-06-13T18:55:00-04:00', idleMinutes: 95 },
    ],
    memory: { total: 32 * GB, used: 21.4 * GB, swapTotal: 8 * GB, swapUsed: 1.2 * GB },
    network: { rxMbps: 5.6, txMbps: 4.1, established: 41 },
  },
  {
    id: 'edge-mac-mini',
    hostname: 'edge-mac-mini.lab.internal',
    ip: '10.0.9.4',
    os: 'macOS',
    osVersion: '15.4 Sequoia',
    agentVersion: '0.7.2',
    status: 'online',
    lastCheckIn: '2026-06-13T20:41:30-04:00',
    uptimeDays: 21,
    tags: ['lab', 'edge'],
    disks: [
      {
        mount: '/',
        device: '/dev/disk0s1',
        fsType: 'apfs',
        total: 512 * GB,
        free: 301 * GB,
        smart: { status: 'PASSED', device: '/dev/disk0', findings: ['No issues detected'] },
        topDirs: [
          { path: '/opt', size: 64 * GB },
          { path: '/Users/agent/Library/Caches', size: 12 * GB },
        ],
        topFiles: [],
      },
    ],
    patches: {
      updatesAvailable: false,
      lastChecked: '2026-06-13T06:00:00-04:00',
      total: 0,
      security: 0,
      summary: 'No updates available',
      packages: [],
    },
    logins: [],
    memory: { total: 16 * GB, used: 6.1 * GB, swapTotal: 0, swapUsed: 0 },
    network: { rxMbps: 1.2, txMbps: 0.6, established: 8 },
  },
  {
    id: 'build01',
    hostname: 'build01.ci.internal',
    ip: '10.0.6.10',
    os: 'Fedora',
    osVersion: '40',
    agentVersion: '0.6.9',
    status: 'offline',
    lastCheckIn: '2026-06-13T14:02:51-04:00',
    uptimeDays: 0,
    tags: ['ci'],
    disks: [
      {
        mount: '/',
        device: '/dev/sda1',
        fsType: 'ext4',
        total: 500 * GB,
        free: 12 * GB,
        smart: { status: 'PASSED', device: '/dev/sda', findings: ['No issues detected'] },
        topDirs: [
          { path: '/var/lib/containers', size: 320 * GB },
          { path: '/home/ci/workspace', size: 140 * GB },
        ],
        topFiles: [],
      },
    ],
    patches: {
      updatesAvailable: true,
      lastChecked: '2026-06-12T06:00:00-04:00',
      total: 22,
      security: 2,
      summary: '22 packages can be updated (2 security)',
      packages: [
        { name: 'kernel', current: '6.8.5-301', candidate: '6.8.9-301', severity: 'high', cve: ['CVE-2026-1875'] },
      ],
    },
    logins: [],
    memory: { total: 32 * GB, used: 4.4 * GB, swapTotal: 8 * GB, swapUsed: 0 },
    network: { rxMbps: 0, txMbps: 0, established: 0 },
    statusNote: 'Agent unreachable since 14:02 – connection refused',
  },
];

export const formatBytes = (bytes) => {
  if (bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  const exp = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  return `${(bytes / 1024 ** exp).toFixed(exp === 0 ? 0 : 1)} ${units[exp]}`;
};

export const usedBytes = (disk) => disk.total - disk.free;
export const usedPct = (disk) => (usedBytes(disk) / disk.total) * 100;

export const memUsedPct = (mem) => (mem.used / mem.total) * 100;
export const swapUsedPct = (mem) => (mem.swapTotal === 0 ? 0 : (mem.swapUsed / mem.swapTotal) * 100);
