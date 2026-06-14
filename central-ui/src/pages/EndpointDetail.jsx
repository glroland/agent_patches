import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import Badge from '../components/Badge';
import Card from '../components/Card';
import ProgressBar from '../components/ProgressBar';
import { ChatIcon } from '../components/icons';
import { endpoints, usedBytes, usedPct, memUsedPct, swapUsedPct, formatBytes } from '../data/endpoints';

const TABS = ['Overview', 'Disks', 'Patches', 'Sessions', 'Interact'];

export default function EndpointDetail() {
  const { id } = useParams();
  const endpoint = endpoints.find((e) => e.id === id);
  const [tab, setTab] = useState('Overview');

  if (!endpoint) {
    return (
      <div className="space-y-4">
        <p className="text-sm text-slate-400">Endpoint not found.</p>
        <Link to="/endpoints" className="text-sm font-medium text-indigo-400 hover:text-indigo-300">&larr; Back to endpoints</Link>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div>
        <Link to="/endpoints" className="text-xs font-medium text-slate-500 hover:text-slate-300">&larr; Endpoints</Link>
        <div className="mt-2 flex flex-wrap items-center justify-between gap-4">
          <div>
            <div className="flex items-center gap-3">
              <h1 className="text-2xl font-semibold text-slate-100">{endpoint.hostname}</h1>
              <Badge variant={endpoint.status}>{endpoint.status}</Badge>
            </div>
            <p className="mt-1 text-sm text-slate-500">
              {endpoint.ip} &middot; {endpoint.os} {endpoint.osVersion} &middot; agent v{endpoint.agentVersion} &middot; uptime {endpoint.uptimeDays}d
            </p>
          </div>
          <div className="flex gap-2">
            {endpoint.tags.map((t) => (
              <span key={t} className="rounded-full bg-slate-800 px-2.5 py-1 text-xs text-slate-400">{t}</span>
            ))}
          </div>
        </div>
        {endpoint.statusNote && (
          <p className="mt-3 rounded-md bg-amber-500/10 px-3 py-2 text-sm text-amber-300 ring-1 ring-inset ring-amber-500/20">
            {endpoint.statusNote}
          </p>
        )}
      </div>

      <div className="flex gap-1 border-b border-slate-800">
        {TABS.map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`relative px-4 py-2.5 text-sm font-medium transition-colors ${
              tab === t ? 'text-indigo-300' : 'text-slate-500 hover:text-slate-300'
            }`}
          >
            {t}
            {tab === t && <span className="absolute inset-x-0 -bottom-px h-0.5 rounded-full bg-indigo-400" />}
          </button>
        ))}
      </div>

      {tab === 'Overview' && <OverviewTab endpoint={endpoint} />}
      {tab === 'Disks' && <DisksTab endpoint={endpoint} />}
      {tab === 'Patches' && <PatchesTab endpoint={endpoint} />}
      {tab === 'Sessions' && <SessionsTab endpoint={endpoint} />}
      {tab === 'Interact' && <InteractTab endpoint={endpoint} />}
    </div>
  );
}

function OverviewTab({ endpoint }) {
  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
      <Card title="Memory">
        <div className="space-y-3">
          <ProgressBar pct={memUsedPct(endpoint.memory)} label={`RAM: ${formatBytes(endpoint.memory.used)} / ${formatBytes(endpoint.memory.total)}`} />
          {endpoint.memory.swapTotal > 0 && (
            <ProgressBar pct={swapUsedPct(endpoint.memory)} label={`Swap: ${formatBytes(endpoint.memory.swapUsed)} / ${formatBytes(endpoint.memory.swapTotal)}`} />
          )}
        </div>
      </Card>

      <Card title="Network">
        <dl className="grid grid-cols-3 gap-4 text-sm">
          <div>
            <dt className="text-xs text-slate-500">Inbound</dt>
            <dd className="mt-1 text-lg font-semibold text-slate-100">{endpoint.network.rxMbps} <span className="text-xs text-slate-500">Mbps</span></dd>
          </div>
          <div>
            <dt className="text-xs text-slate-500">Outbound</dt>
            <dd className="mt-1 text-lg font-semibold text-slate-100">{endpoint.network.txMbps} <span className="text-xs text-slate-500">Mbps</span></dd>
          </div>
          <div>
            <dt className="text-xs text-slate-500">Established</dt>
            <dd className="mt-1 text-lg font-semibold text-slate-100">{endpoint.network.established}</dd>
          </div>
        </dl>
      </Card>

      <Card title="Disk summary" className="lg:col-span-2">
        <div className="space-y-4">
          {endpoint.disks.map((d) => (
            <div key={d.mount}>
              <ProgressBar pct={usedPct(d)} label={`${d.mount} (${d.fsType}) — ${formatBytes(usedBytes(d))} / ${formatBytes(d.total)}`} />
            </div>
          ))}
        </div>
      </Card>
    </div>
  );
}

function DisksTab({ endpoint }) {
  return (
    <div className="space-y-4">
      {endpoint.disks.map((d) => (
        <Card key={d.mount} title={d.mount} subtitle={`${d.device} · ${d.fsType}`}>
          <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
            <div className="space-y-4">
              <ProgressBar pct={usedPct(d)} label={`${formatBytes(usedBytes(d))} used of ${formatBytes(d.total)} (${formatBytes(d.free)} free)`} />

              <div>
                <p className="mb-2 text-xs font-medium uppercase tracking-wide text-slate-500">S.M.A.R.T. status</p>
                {d.smart?.status === 'unavailable' || !d.smart?.device ? (
                  <p className="text-sm text-slate-500">Unavailable on this platform.</p>
                ) : (
                  <div>
                    <Badge variant={d.smart.status === 'FAILED' ? 'failed' : 'passed'}>
                      {d.smart.status} &middot; {d.smart.device}
                    </Badge>
                    <ul className="mt-2 space-y-1 text-sm text-slate-400">
                      {d.smart.findings.map((f) => (
                        <li key={f}>&bull; {f}</li>
                      ))}
                    </ul>
                  </div>
                )}
              </div>
            </div>

            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div>
                <p className="mb-2 text-xs font-medium uppercase tracking-wide text-slate-500">Top directories</p>
                <ul className="space-y-1.5 text-sm">
                  {d.topDirs.map((entry) => (
                    <li key={entry.path} className="flex items-center justify-between gap-2">
                      <span className="truncate text-slate-300" title={entry.path}>{entry.path}</span>
                      <span className="whitespace-nowrap text-slate-500">{formatBytes(entry.size)}</span>
                    </li>
                  ))}
                  {d.topDirs.length === 0 && <li className="text-slate-500">No data.</li>}
                </ul>
              </div>
              <div>
                <p className="mb-2 text-xs font-medium uppercase tracking-wide text-slate-500">Top files</p>
                <ul className="space-y-1.5 text-sm">
                  {d.topFiles.map((entry) => (
                    <li key={entry.path} className="flex items-center justify-between gap-2">
                      <span className="truncate text-slate-300" title={entry.path}>{entry.path}</span>
                      <span className="whitespace-nowrap text-slate-500">{formatBytes(entry.size)}</span>
                    </li>
                  ))}
                  {d.topFiles.length === 0 && <li className="text-slate-500">No data.</li>}
                </ul>
              </div>
            </div>
          </div>
        </Card>
      ))}
    </div>
  );
}

function PatchesTab({ endpoint }) {
  return (
    <Card
      title="Pending updates"
      subtitle={`Last checked ${new Date(endpoint.patches.lastChecked).toLocaleString()}`}
      action={
        <button className="rounded-lg bg-indigo-500 px-3 py-1.5 text-xs font-semibold text-white shadow-sm shadow-indigo-900/40 transition-colors hover:bg-indigo-400">
          Apply updates
        </button>
      }
    >
      {endpoint.patches.total === 0 ? (
        <p className="text-sm text-slate-400">{endpoint.patches.summary}</p>
      ) : (
        <div className="space-y-3">
          <p className="text-sm text-slate-400">{endpoint.patches.summary}</p>
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="text-xs uppercase tracking-wide text-slate-500">
                <th className="pb-2 pr-4">Package</th>
                <th className="pb-2 pr-4">Current</th>
                <th className="pb-2 pr-4">Candidate</th>
                <th className="pb-2 pr-4">Severity</th>
                <th className="pb-2 pr-4">CVEs</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800">
              {endpoint.patches.packages.map((p) => (
                <tr key={p.name} className="text-slate-300">
                  <td className="py-2.5 pr-4 font-medium text-slate-100">{p.name}</td>
                  <td className="py-2.5 pr-4 text-slate-500">{p.current}</td>
                  <td className="py-2.5 pr-4 text-slate-500">{p.candidate}</td>
                  <td className="py-2.5 pr-4">
                    <Badge variant={p.severity === 'critical' || p.severity === 'high' ? 'critical' : p.severity === 'medium' ? 'warning' : 'neutral'}>
                      {p.severity}
                    </Badge>
                  </td>
                  <td className="py-2.5 pr-4 text-slate-500">{p.cve.length ? p.cve.join(', ') : '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {endpoint.patches.total > endpoint.patches.packages.length && (
            <p className="text-xs text-slate-500">
              + {endpoint.patches.total - endpoint.patches.packages.length} additional package(s) not shown.
            </p>
          )}
        </div>
      )}
    </Card>
  );
}

function SessionsTab({ endpoint }) {
  return (
    <Card title="Active interactive sessions" subtitle="From check_interactive_logins">
      {endpoint.logins.length === 0 ? (
        <p className="text-sm text-slate-400">No interactive sessions detected.</p>
      ) : (
        <table className="w-full text-left text-sm">
          <thead>
            <tr className="text-xs uppercase tracking-wide text-slate-500">
              <th className="pb-2 pr-4">User</th>
              <th className="pb-2 pr-4">TTY</th>
              <th className="pb-2 pr-4">Remote host</th>
              <th className="pb-2 pr-4">Since</th>
              <th className="pb-2 pr-4">Idle</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-800">
            {endpoint.logins.map((s) => (
              <tr key={`${s.user}-${s.tty}`} className="text-slate-300">
                <td className="py-2.5 pr-4 font-medium text-slate-100">
                  {s.user}
                  {(s.user === 'root' || s.user === 'Administrator') && (
                    <Badge variant="warning" className="ml-2">privileged</Badge>
                  )}
                </td>
                <td className="py-2.5 pr-4 text-slate-500">{s.tty}</td>
                <td className="py-2.5 pr-4 text-slate-500">{s.remoteHost}</td>
                <td className="py-2.5 pr-4 text-slate-500">{new Date(s.since).toLocaleString()}</td>
                <td className="py-2.5 pr-4 text-slate-500">{s.idleMinutes}m</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Card>
  );
}

const SUGGESTIONS = [
  'Check for pending patches',
  'Show disk usage summary',
  'List active interactive logins',
  'Run a SMART health check on all disks',
];

function InteractTab({ endpoint }) {
  const [messages, setMessages] = useState([
    { role: 'agent', text: `Connected to ${endpoint.hostname}. Ask me to check status, run diagnostics, or apply patches.` },
  ]);
  const [input, setInput] = useState('');

  const send = (text) => {
    const value = text ?? input;
    if (!value.trim()) return;
    setMessages((prev) => [...prev, { role: 'user', text: value }]);
    setInput('');
    setTimeout(() => {
      setMessages((prev) => [...prev, { role: 'agent', text: mockReply(value, endpoint) }]);
    }, 500);
  };

  return (
    <Card title={`Talk to ${endpoint.hostname}`} subtitle="Sends a task to the endpoint agent via the A2A JSON-RPC API (mocked)">
      <div className="flex h-96 flex-col">
        <div className="flex-1 space-y-3 overflow-y-auto pr-1">
          {messages.map((m, i) => (
            <div key={i} className={`flex ${m.role === 'user' ? 'justify-end' : 'justify-start'}`}>
              <div
                className={`max-w-[80%] rounded-xl px-3.5 py-2.5 text-sm whitespace-pre-wrap ${
                  m.role === 'user'
                    ? 'bg-indigo-500 text-white'
                    : 'bg-slate-800 text-slate-200'
                }`}
              >
                {m.text}
              </div>
            </div>
          ))}
        </div>

        <div className="mt-3 flex flex-wrap gap-2">
          {SUGGESTIONS.map((s) => (
            <button
              key={s}
              onClick={() => send(s)}
              className="rounded-full border border-slate-700 px-3 py-1 text-xs text-slate-400 hover:border-indigo-500/50 hover:text-indigo-300"
            >
              {s}
            </button>
          ))}
        </div>

        <form
          onSubmit={(e) => { e.preventDefault(); send(); }}
          className="mt-3 flex items-center gap-2 border-t border-slate-800 pt-3"
        >
          <ChatIcon className="h-4 w-4 text-slate-500" />
          <input
            value={input}
            onChange={(e) => setInput(e.target.value)}
            placeholder="Send a task to this agent..."
            className="flex-1 rounded-lg border border-slate-800 bg-slate-900/60 px-3 py-2 text-sm text-slate-200 placeholder:text-slate-500 focus:border-indigo-500 focus:outline-none"
          />
          <button type="submit" className="rounded-lg bg-indigo-500 px-4 py-2 text-sm font-semibold text-white hover:bg-indigo-400">
            Send
          </button>
        </form>
      </div>
    </Card>
  );
}

function mockReply(message, endpoint) {
  const lower = message.toLowerCase();
  if (lower.includes('patch')) {
    return endpoint.patches.total === 0
      ? 'No updates are currently available — this host is up to date.'
      : `${endpoint.patches.summary}. Reply "apply updates" to proceed (mocked — no changes will be made).`;
  }
  if (lower.includes('disk')) {
    return endpoint.disks
      .map((d) => `${d.mount}: ${formatBytes(usedBytes(d))} used of ${formatBytes(d.total)} (${usedPct(d).toFixed(1)}%)`)
      .join('\n');
  }
  if (lower.includes('login') || lower.includes('session')) {
    return endpoint.logins.length === 0
      ? 'No interactive sessions are currently active.'
      : endpoint.logins.map((s) => `${s.user} on ${s.tty} from ${s.remoteHost} (idle ${s.idleMinutes}m)`).join('\n');
  }
  if (lower.includes('smart')) {
    return endpoint.disks
      .map((d) => `${d.device || d.mount}: ${d.smart?.status ?? 'unavailable'}`)
      .join('\n');
  }
  return `Acknowledged: "${message}" (this is a mock response — no live agent connection in this build).`;
}
