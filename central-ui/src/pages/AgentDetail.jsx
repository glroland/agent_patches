import { useState, useEffect, useRef } from 'react';
import { Link, useParams, useSearchParams } from 'react-router-dom';
import Badge from '../components/Badge';
import Card from '../components/Card';
import StatCard from '../components/StatCard';
import TimelineEntry from '../components/TimelineEntry';
import AsyncState from '../components/AsyncState';
import { ChatIcon, CheckIcon, XIcon, TrashIcon, ChevronRightIcon, TerminalIcon } from '../components/icons';
import { fetchAgent, fetchAgentMemory, fetchAgentResponsibilities, fetchAgentLog, fetchAgentCard, fetchAgentNetworkConnections, fetchAgentInteractiveLogins, clearAgentMemory, decideApproval } from '../api/client';
import { useApi } from '../hooks/useApi';
import { useFleetSocket } from '../hooks/useFleetSocket';
import { useChatHistory } from '../hooks/useChatHistory';
import { relativeTime } from '../utils/time';

const TABS = ['Interact', 'Recommendations & Approvals', 'Activity', 'Responsibilities', 'Network', 'Logins', 'Log', 'Agent Card', 'Admin'];

export default function AgentDetail() {
  const { id } = useParams();
  const [searchParams] = useSearchParams();
  const { data: agent, loading, error } = useApi(() => fetchAgent(id), [id]);
  const [tab, setTab] = useState(
    searchParams.get('tab') === 'approvals' ? 'Recommendations & Approvals' : 'Interact'
  );
  const [approvalState, setApprovalState] = useState({});

  if (loading) {
    return <AsyncState loading loadingLabel="Loading agent..." />;
  }

  if (error?.message?.includes('404') || (!loading && !error && !agent)) {
    return (
      <div className="space-y-4">
        <p className="text-sm text-navy-600">Agent not found.</p>
        <Link to="/agents" className="text-sm font-medium text-navy-600 hover:text-navy-800">&larr; Back to agents</Link>
      </div>
    );
  }

  if (error) {
    return <AsyncState error={error} />;
  }

  const decide = (entryId, decision) => {
    setApprovalState((prev) => ({ ...prev, [entryId]: decision }));
  };

  const resolveStatus = (entry) => approvalState[entry.id] ?? entry.status;

  return (
    <div className="space-y-6">
      <div>
        <Link to="/agents" className="text-xs font-medium text-navy-500 hover:text-navy-900">&larr; Agents</Link>
        <div className="mt-2 flex flex-wrap items-center justify-between gap-4">
          <div>
            <div className="flex items-center gap-3">
              <h1 className="text-2xl font-semibold text-black">{agent.hostname}</h1>
              <Badge variant={agent.status}>{agent.statusLabel}</Badge>
            </div>
            <p className="mt-1 text-sm text-navy-500">
              {agent.role} &middot; {agent.os}
              {agent.buildTime && <> &middot; built {agent.buildTime}</>}
              {' '}&middot; last polled {relativeTime(agent.lastPoll)}
            </p>
          </div>
          <div className="flex gap-2">
            {agent.tags.map((t) => (
              <span key={t} className="rounded-full bg-navy-100 px-2.5 py-1 text-xs text-navy-600">{t}</span>
            ))}
          </div>
        </div>
        <div className="mt-3 rounded-md bg-white px-3 py-2 text-sm text-navy-700 ring-1 ring-inset ring-navy-200">
          <span className="text-xs font-medium uppercase tracking-wide text-navy-500">
            {agent.currentTask ? 'Currently working on' : 'Status'}
          </span>
          <p className="mt-0.5">{agent.currentTask ?? agent.statusDescription}</p>
        </div>
      </div>

      {agent.diskTrends && Object.keys(agent.diskTrends).length > 0 && (
        <DiskTrendsPanel trends={agent.diskTrends} />
      )}

      {agent.smartTrends && Object.keys(agent.smartTrends).length > 0 && (
        <SmartTrendsPanel trends={agent.smartTrends} />
      )}

      <div className="flex gap-1 border-b border-navy-200">
        {TABS.map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`relative px-4 py-2.5 text-sm font-medium transition-colors ${
              tab === t ? 'text-navy-700' : 'text-navy-500 hover:text-navy-900'
            }`}
          >
            {t}
            {tab === t && <span className="absolute inset-x-0 -bottom-px h-0.5 rounded-full bg-navy-600" />}
          </button>
        ))}
      </div>

      {tab === 'Activity' && (
        <Card title="Activity log" subtitle="Everything this agent has observed, done, or recommended, most recent first">
          <div className="space-y-5">
            {agent.timeline.map((entry) => (
              <TimelineEntry key={entry.id} entry={entry} />
            ))}
          </div>
        </Card>
      )}

      {tab === 'Recommendations & Approvals' && (
        <RecommendationsTab agent={agent} resolveStatus={resolveStatus} decide={decide} />
      )}

      {tab === 'Interact' && <InteractTab agent={agent} />}

      {tab === 'Responsibilities' && <ResponsibilitiesTab agentId={agent.id} />}

      {tab === 'Network' && <NetworkConnectionsTab agentId={agent.id} />}

      {tab === 'Logins' && <InteractiveLoginsTab agentId={agent.id} />}

      {tab === 'Log' && <AgentLogTab agentId={agent.id} />}

      {tab === 'Agent Card' && <AgentCardTab agent={agent} />}

      {tab === 'Admin' && <AgentAdminTab agent={agent} />}
    </div>
  );
}

function RecommendationsTab({ agent, resolveStatus, decide }) {
  const approvals = agent.timeline.filter((t) => t.type === 'approval');
  const manualRuns = agent.timeline.filter((t) => t.type === 'manual_run' && t.status === 'pending');
  const recommendations = agent.timeline.filter((t) => t.type === 'recommendation');

  return (
    <div className="space-y-4">
      <Card title="Awaiting your decision" subtitle="The agent will act once you approve or reject these">
        {approvals.length === 0 ? (
          <p className="text-sm text-navy-500">This agent has no actions waiting on approval.</p>
        ) : (
          <div className="space-y-4">
            {approvals.map((entry) => {
              const status = resolveStatus(entry);
              return (
                <div key={entry.id} className="rounded-lg border border-navy-200 p-4">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant={entry.risk}>{entry.risk} risk</Badge>
                    <Badge variant={status}>{status}</Badge>
                    <span className="text-xs text-navy-400">{relativeTime(entry.time)}</span>
                  </div>
                  <p className="mt-2 text-sm font-medium text-navy-900">{entry.title}</p>
                  <p className="mt-1 text-sm text-navy-500">{entry.detail}</p>
                  <p className="mt-2 rounded-md bg-navy-50 px-3 py-2 font-mono text-xs text-navy-600">
                    {entry.proposedAction}
                  </p>
                  {status === 'pending' ? (
                    <div className="mt-3 flex gap-2">
                      <button
                        onClick={() => decide(entry.id, 'approved')}
                        className="inline-flex items-center gap-1.5 rounded-lg bg-emerald-500 px-3 py-1.5 text-xs font-semibold text-white hover:bg-emerald-600"
                      >
                        <CheckIcon className="h-3.5 w-3.5" /> Approve
                      </button>
                      <button
                        onClick={() => decide(entry.id, 'rejected')}
                        className="inline-flex items-center gap-1.5 rounded-lg border border-navy-300 px-3 py-1.5 text-xs font-semibold text-navy-700 hover:border-rose-400 hover:text-rose-800"
                      >
                        <XIcon className="h-3.5 w-3.5" /> Reject
                      </button>
                    </div>
                  ) : (
                    <p className="mt-3 text-xs text-navy-500">
                      {status === 'approved'
                        ? 'The agent will carry this out and report back on the next poll.'
                        : 'The agent will not proceed with this action.'}
                    </p>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </Card>

      {manualRuns.length > 0 && (
        <Card title="Manual runs needed" subtitle="Commands blocked by sudoers — run them manually and paste the output on the Approvals page">
          <div className="space-y-3">
            {manualRuns.map((entry) => (
              <div key={entry.id} className="rounded-lg border border-orange-200 bg-orange-50/40 p-4">
                <div className="flex items-center gap-2">
                  <TerminalIcon className="h-4 w-4 text-orange-600" />
                  <p className="text-sm font-medium text-navy-900">{entry.title}</p>
                  <span className="text-xs text-navy-400">{relativeTime(entry.time)}</span>
                </div>
                <p className="mt-1 text-sm text-navy-500">{entry.detail}</p>
                <p className="mt-2 rounded-md bg-navy-800 px-3 py-2 font-mono text-xs text-green-300">
                  {entry.proposedAction}
                </p>
                <Link
                  to="/approvals"
                  className="mt-3 inline-block text-xs font-medium text-orange-700 underline hover:text-orange-900"
                >
                  Submit output on the Approvals page →
                </Link>
              </div>
            ))}
          </div>
        </Card>
      )}

      <Card title="Other recommendations" subtitle="Suggestions that don't require a specific action right now">
        {recommendations.length === 0 ? (
          <p className="text-sm text-navy-500">No open recommendations.</p>
        ) : (
          <div className="space-y-5">
            {recommendations.map((entry) => (
              <TimelineEntry key={entry.id} entry={entry} />
            ))}
          </div>
        )}
      </Card>
    </div>
  );
}

const SUGGESTIONS = [
  'What are you working on right now?',
  'Summarize what you have seen recently',
  'Why do you need approval for that?',
  'Re-check everything now',
];

function InteractTab({ agent }) {
  const { messages, sending, hydrated, addMessage, clearChat, sendToAgent } = useChatHistory(agent.id);
  const [input, setInput] = useState('');
  // Per-approval decision state: id -> { loading, decision, error }
  const [approvalDecisions, setApprovalDecisions] = useState({});
  const bottomRef = useRef(null);

  const greeting = {
    role: 'agent',
    text: `Hi, I'm the agent for ${agent.hostname}. Ask me about what I've seen, what I'm doing, or why I've made a recommendation.`,
  };

  // IDs of approvals that existed when this tab mounted — we only inject
  // approvals that appear after the user sends a message.
  const knownApprovalIds = useRef(
    new Set(agent.timeline.filter((e) => e.type === 'approval').map((e) => e.id))
  );

  // Watch live approval count for this agent from the WebSocket.
  const { agents: wsAgents } = useFleetSocket();
  const wsApprovalCount = wsAgents?.find((a) => a.id === agent.id)?.pendingApprovalCount ?? 0;

  // Greet on first visit only — wait for the persisted history to hydrate so
  // a restored conversation isn't prefixed with a duplicate greeting.
  useEffect(() => {
    if (hydrated && messages.length === 0) {
      addMessage(greeting);
    }
  }, [hydrated]); // eslint-disable-line react-hooks/exhaustive-deps

  // Auto-scroll to latest message.
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages, sending]);

  // While waiting for an agent reply, watch for new pending approvals that
  // appear in the WS fleet data and inject them inline so the user never has
  // to leave the chat to resolve them.
  useEffect(() => {
    if (!sending) return;
    fetchAgent(agent.id)
      .then((fullAgent) => {
        const newApprovals = (fullAgent.timeline ?? []).filter(
          (e) =>
            e.type === 'approval' &&
            e.status === 'pending' &&
            !knownApprovalIds.current.has(e.id)
        );
        newApprovals.forEach((entry) => {
          knownApprovalIds.current.add(entry.id);
          addMessage({ role: 'approval', entry });
        });
      })
      .catch(() => {});
  }, [sending, wsApprovalCount, agent.id, addMessage]);

  const send = (text) => {
    const value = (text ?? input).trim();
    if (!value || sending) return;
    setInput('');
    sendToAgent(agent.id, value);
  };

  const clear = () => {
    clearChat();
    addMessage(greeting);
  };

  const resolveApproval = async (entry, decision) => {
    setApprovalDecisions((prev) => ({ ...prev, [entry.id]: { loading: true } }));
    try {
      await decideApproval(entry.id, decision, agent.id);
      setApprovalDecisions((prev) => ({ ...prev, [entry.id]: { decision } }));
    } catch (err) {
      if (err.message.includes('404') || err.message.toLowerCase().includes('not found')) {
        setApprovalDecisions((prev) => ({ ...prev, [entry.id]: { decision: 'stale' } }));
      } else {
        setApprovalDecisions((prev) => ({ ...prev, [entry.id]: { error: err.message } }));
      }
    }
  };

  return (
    <Card
      title={`Talk to the ${agent.hostname} agent`}
      subtitle="Sends a message via the A2A JSON-RPC API"
      action={
        messages.length > 0 && (
          <button
            onClick={clear}
            disabled={sending}
            title="Clear chat history"
            className="inline-flex items-center gap-1.5 rounded-lg border border-navy-300 px-2.5 py-1.5 text-xs font-medium text-navy-600 hover:border-navy-400 hover:text-navy-800 disabled:opacity-50 transition-colors"
          >
            <TrashIcon className="h-3.5 w-3.5" /> Clear
          </button>
        )
      }
    >
      <div className="flex h-[32rem] flex-col">
        <div className="flex-1 space-y-3 overflow-y-auto pr-1">
          {messages.map((m, i) => {
            if (m.role === 'approval') {
              const state = approvalDecisions[m.entry.id];
              const decided = state?.decision;
              return (
                <div key={i} className="flex justify-start">
                  <div className="w-full max-w-[92%] rounded-xl border border-amber-300 bg-amber-50 p-3 text-sm">
                    <div className="mb-2 flex items-center gap-2">
                      <span className="text-xs font-semibold uppercase tracking-wide text-amber-700">
                        Approval Required
                      </span>
                      <Badge variant={m.entry.risk}>{m.entry.risk} risk</Badge>
                    </div>
                    <p className="font-medium text-navy-900">{m.entry.title}</p>
                    <p className="mt-1 text-xs text-navy-600">{m.entry.detail}</p>
                    {m.entry.proposedAction && (
                      <p className="mt-2 rounded bg-navy-50 px-2 py-1.5 font-mono text-xs text-navy-600">
                        {m.entry.proposedAction}
                      </p>
                    )}
                    {decided ? (
                      <p className={`mt-2 text-xs font-medium ${decided === 'approved' ? 'text-emerald-700' : decided === 'stale' ? 'text-navy-500' : 'text-rose-700'}`}>
                        {decided === 'approved'
                          ? '✓ Approved — agent will proceed'
                          : decided === 'stale'
                          ? 'No longer pending — already resolved or expired.'
                          : '✗ Rejected — agent will not proceed'}
                      </p>
                    ) : state?.error ? (
                      <p className="mt-2 text-xs text-rose-700">{state.error}</p>
                    ) : (
                      <div className="mt-3 flex gap-2">
                        <button
                          onClick={() => resolveApproval(m.entry, 'approved')}
                          disabled={state?.loading}
                          className="inline-flex items-center gap-1.5 rounded-lg bg-emerald-500 px-3 py-1.5 text-xs font-semibold text-white hover:bg-emerald-600 disabled:cursor-not-allowed disabled:opacity-50"
                        >
                          <CheckIcon className="h-3.5 w-3.5" /> Approve
                        </button>
                        <button
                          onClick={() => resolveApproval(m.entry, 'rejected')}
                          disabled={state?.loading}
                          className="inline-flex items-center gap-1.5 rounded-lg border border-navy-300 px-3 py-1.5 text-xs font-semibold text-navy-700 hover:border-rose-400 hover:text-rose-800 disabled:cursor-not-allowed disabled:opacity-50"
                        >
                          <XIcon className="h-3.5 w-3.5" /> Reject
                        </button>
                      </div>
                    )}
                  </div>
                </div>
              );
            }

            return (
              <div key={i} className={`flex ${m.role === 'user' ? 'justify-end' : 'justify-start'}`}>
                <div
                  className={`max-w-[80%] rounded-xl px-3.5 py-2.5 text-sm whitespace-pre-wrap ${
                    m.role === 'user' ? 'bg-navy-700 text-white' : 'bg-navy-100 text-navy-900'
                  }`}
                >
                  {m.text}
                </div>
              </div>
            );
          })}
          {sending && (
            <div className="flex justify-start">
              <div className="max-w-[80%] rounded-xl bg-navy-100 px-3.5 py-2.5 text-sm text-navy-600">
                Thinking...
              </div>
            </div>
          )}
          <div ref={bottomRef} />
        </div>

        <div className="mt-3 flex flex-wrap gap-2">
          {SUGGESTIONS.map((s) => (
            <button
              key={s}
              onClick={() => send(s)}
              disabled={sending}
              className="rounded-full border border-navy-300 px-3 py-1 text-xs text-navy-600 hover:border-navy-400 hover:text-navy-800 disabled:opacity-50"
            >
              {s}
            </button>
          ))}
        </div>

        <form
          onSubmit={(e) => { e.preventDefault(); send(); }}
          className="mt-3 flex items-center gap-2 border-t border-navy-200 pt-3"
        >
          <ChatIcon className="h-4 w-4 text-navy-500" />
          <input
            value={input}
            onChange={(e) => setInput(e.target.value)}
            placeholder="Ask this agent something..."
            disabled={sending}
            className="flex-1 rounded-lg border border-navy-200 bg-white px-3 py-2 text-sm text-navy-900 placeholder:text-navy-400 focus:border-navy-500 focus:outline-none disabled:opacity-50"
          />
          <button
            type="submit"
            disabled={sending}
            className="rounded-lg bg-navy-700 px-4 py-2 text-sm font-semibold text-white hover:bg-navy-800 disabled:opacity-50"
          >
            Send
          </button>
        </form>
      </div>
    </Card>
  );
}

function CollapsibleSection({ title, subtitle, children }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="rounded-lg border border-navy-200">
      <button
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-center gap-3 px-4 py-3 text-left hover:bg-navy-50 transition-colors rounded-lg"
      >
        <ChevronRightIcon
          className={`h-4 w-4 shrink-0 text-navy-500 transition-transform duration-150 ${open ? 'rotate-90' : ''}`}
        />
        <div className="min-w-0 flex-1">
          <p className="text-sm font-medium text-navy-900">{title}</p>
          {subtitle && <p className="mt-0.5 text-xs text-navy-500 truncate">{subtitle}</p>}
        </div>
      </button>
      {open && (
        <div className="border-t border-navy-200 px-4 pb-4 pt-3">
          {children}
        </div>
      )}
    </div>
  );
}

const STATUS_STYLES = {
  never:   'bg-navy-100 text-navy-600',
  ok:      'bg-emerald-50 text-emerald-700',
  error:   'bg-rose-50 text-rose-700',
  running: 'bg-amber-50 text-amber-700',
};

function ResponsibilitiesTab({ agentId }) {
  const [responsibilities, setResponsibilities] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [expanded, setExpanded] = useState({});

  const load = () => {
    fetchAgentResponsibilities(agentId)
      .then((data) => { setResponsibilities(data); setLoading(false); })
      .catch((err) => { setError(err); setLoading(false); });
  };

  useEffect(() => {
    load();
    const timer = setInterval(load, 30_000);
    return () => clearInterval(timer);
  }, [agentId]); // eslint-disable-line react-hooks/exhaustive-deps

  const toggle = (name) => setExpanded((prev) => ({ ...prev, [name]: !prev[name] }));

  if (loading) return <AsyncState loading loadingLabel="Loading responsibilities..." />;
  if (error) return <AsyncState error={error} />;

  if (!responsibilities || responsibilities.length === 0) {
    return (
      <Card title="Responsibilities" subtitle="Recurring duties assigned to this agent">
        <p className="text-sm text-navy-500">No responsibilities configured.</p>
      </Card>
    );
  }

  return (
    <Card title="Responsibilities" subtitle="Recurring duties assigned to this agent — refreshes every 30s">
      <div className="space-y-3">
        {responsibilities.map((r) => (
          <div key={r.name} className="rounded-lg border border-navy-200">
            <div className="flex flex-wrap items-center gap-3 px-4 py-3">
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <p className="text-sm font-medium text-navy-900">{r.name}</p>
                  <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${STATUS_STYLES[r.status] ?? STATUS_STYLES.never}`}>
                    {r.status}
                  </span>
                  <span className="rounded-full bg-navy-100 px-2 py-0.5 text-xs text-navy-600">{r.schedule}</span>
                </div>
                <div className="mt-1 flex flex-wrap gap-4 text-xs text-navy-500">
                  <span>Last run: {r.lastRunAt ? relativeTime(r.lastRunAt) : 'never'}</span>
                  <span>Next run: {r.nextRunAt ? relativeTime(r.nextRunAt) : '—'}</span>
                  {r.tools && r.tools.length > 0 && (
                    <span>Tools: {r.tools.join(', ')}</span>
                  )}
                </div>
                {r.summary && (
                  <p className="mt-1.5 text-xs text-navy-600 line-clamp-2">{r.summary}</p>
                )}
              </div>
              <button
                onClick={() => toggle(r.name)}
                className="shrink-0 rounded-md border border-navy-300 px-2.5 py-1 text-xs text-navy-600 hover:border-navy-400 hover:text-navy-800 transition-colors"
              >
                {expanded[r.name] ? 'Hide' : 'Show'} instruction
              </button>
            </div>
            {expanded[r.name] && (
              <div className="border-t border-navy-200 px-4 pb-4 pt-3">
                <p className="mb-1.5 text-xs font-semibold uppercase tracking-wide text-navy-500">Instruction</p>
                <pre className="whitespace-pre-wrap rounded-md bg-navy-50 p-3 text-xs text-navy-700 leading-relaxed">
                  {r.instruction}
                </pre>
              </div>
            )}
          </div>
        ))}
      </div>
    </Card>
  );
}

const EVENT_COLORS = {
  open: 'text-emerald-700',
  close: 'text-rose-700',
  existing: 'text-navy-600',
};

const DIRECTION_BADGE = {
  inbound: 'info',
  outbound: 'neutral',
};

const UNUSUAL_CONN_REASON_LABELS = {
  new_inbound_port: 'this local port has never accepted inbound traffic before',
  new_process: 'this process has never made a network connection before',
  new_remote_host: 'this remote host has never been seen before',
};

function unusualConnReasonLabel(reason) {
  return UNUSUAL_CONN_REASON_LABELS[reason] || "deviates from this host's connection history";
}

function NetworkConnectionsTab({ agentId }) {
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  const load = () => {
    fetchAgentNetworkConnections(agentId)
      .then((d) => { setData(d); setLoading(false); })
      .catch((err) => { setError(err); setLoading(false); });
  };

  useEffect(() => {
    load();
    const timer = setInterval(load, 15_000);
    return () => clearInterval(timer);
  }, [agentId]); // eslint-disable-line react-hooks/exhaustive-deps

  if (loading) return <AsyncState loading loadingLabel="Loading network connections..." />;
  if (error) return <AsyncState error={error} />;
  if (!data) return null;

  const active = data.active ?? [];
  const inbound = active.filter((c) => c.direction === 'inbound');
  const outbound = active.filter((c) => c.direction === 'outbound');
  const recent = data.recentActivity ?? [];

  return (
    <div className="space-y-4">
      <Card
        title="Active connections"
        subtitle={
          data.live
            ? 'Live snapshot — the background connection monitor has not recorded any history yet'
            : `Tracked by a background monitor that polls every few seconds · ${data.historyCount} history event${data.historyCount === 1 ? '' : 's'} recorded`
        }
      >
        {data.note && <p className="mb-3 text-xs italic text-navy-500">{data.note}</p>}

        <div className="mb-4 grid grid-cols-3 gap-3">
          <StatCard label="Active" value={active.length} />
          <StatCard label="Inbound" value={inbound.length} />
          <StatCard label="Outbound" value={outbound.length} />
        </div>

        {active.length === 0 ? (
          <p className="text-sm text-navy-500">No active connections.</p>
        ) : (
          <div className="overflow-x-auto rounded-lg border border-navy-200">
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b border-navy-200 text-left text-navy-500">
                  <th className="px-3 py-2 font-medium">Direction</th>
                  <th className="px-3 py-2 font-medium">Proto</th>
                  <th className="px-3 py-2 font-medium">Local</th>
                  <th className="px-3 py-2 font-medium">Remote</th>
                  <th className="px-3 py-2 font-medium">State</th>
                  <th className="px-3 py-2 font-medium">Process</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-navy-100">
                {active.map((c, i) => (
                  <tr key={i} className="hover:bg-navy-50">
                    <td className="px-3 py-2">
                      <Badge variant={DIRECTION_BADGE[c.direction] ?? 'neutral'}>{c.direction}</Badge>
                    </td>
                    <td className="px-3 py-2 uppercase text-navy-600">{c.proto}</td>
                    <td className="px-3 py-2 font-mono text-navy-700">{c.localAddr}:{c.localPort}</td>
                    <td className="px-3 py-2 font-mono text-navy-700">{c.remoteAddr}:{c.remotePort}</td>
                    <td className="px-3 py-2 text-navy-500">{c.state || '—'}</td>
                    <td className="px-3 py-2 text-navy-500">{c.process || (c.pid ? `pid ${c.pid}` : '—')}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      {!data.live && (
        <Card title="Recent activity" subtitle="Connection open/close events, most recent first">
          {recent.length === 0 ? (
            <p className="text-sm text-navy-500">No recent activity.</p>
          ) : (
            <div className="space-y-1">
              {recent.map((e, i) => (
                <div key={i} className="flex flex-wrap items-center gap-2 rounded-md px-2 py-1.5 text-xs hover:bg-navy-50">
                  <span className="w-32 shrink-0 text-navy-400">{relativeTime(e.timestamp)}</span>
                  <span className={`w-14 shrink-0 font-medium ${EVENT_COLORS[e.eventType] ?? 'text-navy-600'}`}>{e.eventType}</span>
                  <span className="w-16 shrink-0 text-navy-500">{e.direction}</span>
                  <span className="font-mono text-navy-700">
                    {e.proto?.toUpperCase()} {e.localAddr}:{e.localPort} &rarr; {e.remoteAddr}:{e.remotePort}
                  </span>
                  {(e.process || e.pid) && (
                    <span className="text-navy-400">({e.process || `pid ${e.pid}`})</span>
                  )}
                  {e.unusual && (
                    <span title={unusualConnReasonLabel(e.unusualReason)}>
                      <Badge variant="warning">unusual</Badge>
                    </span>
                  )}
                </div>
              ))}
            </div>
          )}
        </Card>
      )}
    </div>
  );
}

const LOGIN_EVENT_COLORS = {
  login: 'text-emerald-700',
  logout: 'text-rose-700',
  existing: 'text-navy-600',
};

function originLabel(item) {
  if (!item.remote) return 'local console';
  const parts = [item.sourceIp || item.remoteHost].filter(Boolean);
  if (item.resolvedHostname && item.resolvedHostname !== item.remoteHost && item.resolvedHostname !== item.sourceIp) {
    parts.push(item.resolvedHostname);
  }
  return parts.length > 0 ? parts.join(' · ') : 'remote';
}

const UNUSUAL_REASON_LABELS = {
  new_user: 'first-ever login for this user',
  new_source: 'never seen this source for this user before',
  unusual_time: 'a time of day this user has never logged in at',
};

function unusualReasonLabel(reason) {
  return UNUSUAL_REASON_LABELS[reason] || 'deviates from this host\'s login history';
}

function InteractiveLoginsTab({ agentId }) {
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  const load = () => {
    fetchAgentInteractiveLogins(agentId)
      .then((d) => { setData(d); setLoading(false); })
      .catch((err) => { setError(err); setLoading(false); });
  };

  useEffect(() => {
    load();
    const timer = setInterval(load, 15_000);
    return () => clearInterval(timer);
  }, [agentId]); // eslint-disable-line react-hooks/exhaustive-deps

  if (loading) return <AsyncState loading loadingLabel="Loading interactive logins..." />;
  if (error) return <AsyncState error={error} />;
  if (!data) return null;

  const active = data.active ?? [];
  const recent = data.recentActivity ?? [];
  const failed = data.recentFailedAttempts ?? [];

  return (
    <div className="space-y-4">
      <Card
        title="Active sessions"
        subtitle={
          data.live
            ? 'Live snapshot — the background login monitor has not recorded any history yet'
            : `Tracked by background login monitors · ${data.historyCount} history event${data.historyCount === 1 ? '' : 's'} recorded`
        }
      >
        {data.note && <p className="mb-3 text-xs italic text-navy-500">{data.note}</p>}

        <div className="mb-4 grid grid-cols-2 gap-3 sm:grid-cols-3">
          <StatCard label="Active sessions" value={active.length} />
          <StatCard label="History events" value={data.historyCount} />
          <StatCard label="Failed attempts" value={data.failedCount} tone={data.failedCount > 0 ? 'warning' : 'default'} />
        </div>

        {active.length === 0 ? (
          <p className="text-sm text-navy-500">No active login sessions.</p>
        ) : (
          <div className="overflow-x-auto rounded-lg border border-navy-200">
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b border-navy-200 text-left text-navy-500">
                  <th className="px-3 py-2 font-medium">User</th>
                  <th className="px-3 py-2 font-medium">Class</th>
                  <th className="px-3 py-2 font-medium">Type / TTY</th>
                  <th className="px-3 py-2 font-medium">Origin</th>
                  <th className="px-3 py-2 font-medium">Since</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-navy-100">
                {active.map((s) => (
                  <tr key={s.sessionId} className="hover:bg-navy-50">
                    <td className="px-3 py-2 font-medium text-navy-900">{s.username}</td>
                    <td className="px-3 py-2 text-navy-500">{s.class || '—'}</td>
                    <td className="px-3 py-2 text-navy-500">{s.tty || s.sessionType || '—'}</td>
                    <td className="px-3 py-2">
                      <Badge variant={s.remote ? 'info' : 'neutral'}>{originLabel(s)}</Badge>
                    </td>
                    <td className="px-3 py-2 text-navy-500">{relativeTime(s.timestamp)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      {!data.live && (
        <Card title="Recent activity" subtitle="Login/logout events, most recent first">
          {recent.length === 0 ? (
            <p className="text-sm text-navy-500">No recent activity.</p>
          ) : (
            <div className="space-y-1">
              {recent.map((e, i) => (
                <div key={i} className="flex flex-wrap items-center gap-2 rounded-md px-2 py-1.5 text-xs hover:bg-navy-50">
                  <span className="w-32 shrink-0 text-navy-400">{relativeTime(e.timestamp)}</span>
                  <span className={`w-14 shrink-0 font-medium ${LOGIN_EVENT_COLORS[e.eventType] ?? 'text-navy-600'}`}>{e.eventType}</span>
                  <span className="font-medium text-navy-900">{e.username}</span>
                  <span className="text-navy-500">{e.tty || e.sessionType || ''}</span>
                  <span className="text-navy-400">({originLabel(e)})</span>
                  {e.unusual && (
                    <span title={unusualReasonLabel(e.unusualReason)}>
                      <Badge variant="warning">unusual</Badge>
                    </span>
                  )}
                </div>
              ))}
            </div>
          )}
        </Card>
      )}

      <Card title="Recent failed login attempts" subtitle="Most recent first">
        {failed.length === 0 ? (
          <p className="text-sm text-navy-500">No failed login attempts recorded.</p>
        ) : (
          <div className="space-y-1">
            {failed.map((f, i) => (
              <div key={i} className="flex flex-wrap items-center gap-2 rounded-md px-2 py-1.5 text-xs hover:bg-rose-50">
                <span className="w-32 shrink-0 text-navy-400">{relativeTime(f.timestamp)}</span>
                <span className="font-medium text-navy-900">{f.username}</span>
                <span className="font-mono text-navy-600">{f.sourceIp || f.remoteHost || 'unknown source'}</span>
                {f.resolvedHostname && f.resolvedHostname !== f.remoteHost && f.resolvedHostname !== f.sourceIp && (
                  <span className="text-navy-400">({f.resolvedHostname})</span>
                )}
                <span className="text-rose-700">{f.reason}</span>
                {f.service && <span className="text-navy-400">via {f.service}</span>}
              </div>
            ))}
          </div>
        )}
      </Card>
    </div>
  );
}

function DiskTrendsPanel({ trends }) {
  const mounts = Object.values(trends).filter((t) => t.samples && t.samples.length > 0);
  if (mounts.length === 0) return null;

  return (
    <Card title="Disk trends" subtitle="7-day rolling growth rate per mount — updates every check_drives run">
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {mounts.map((t) => {
          const current = t.samples[t.samples.length - 1];
          const pct = current?.usedPct ?? 0;
          const slope = t.slopePerDay ?? 0;
          const forecast = t.forecastDays ?? -1;

          let trendLabel;
          let trendColor;
          if (slope <= 0.1) {
            trendLabel = '→ stable';
            trendColor = 'text-navy-600';
          } else if (forecast > 0 && forecast < 7) {
            trendLabel = `↑ ${slope.toFixed(1)}%/day — fills in ~${forecast}d`;
            trendColor = 'text-rose-700';
          } else if (forecast > 0 && forecast < 30) {
            trendLabel = `↑ ${slope.toFixed(1)}%/day — fills in ~${forecast}d`;
            trendColor = 'text-amber-700';
          } else if (slope > 0.1) {
            trendLabel = `↑ ${slope.toFixed(1)}%/day`;
            trendColor = 'text-navy-600';
          }

          const barColor = pct >= 90 ? 'bg-rose-500' : pct >= 80 ? 'bg-amber-500' : 'bg-emerald-500';

          return (
            <div key={t.mount} className="rounded-lg border border-navy-200 bg-navy-50 p-3">
              <p className="truncate text-xs font-medium text-navy-700" title={t.mount}>{t.mount}</p>
              <div className="mt-2 flex items-center gap-2">
                <div className="h-1.5 flex-1 rounded-full bg-navy-100">
                  <div className={`h-1.5 rounded-full ${barColor}`} style={{ width: `${Math.min(pct, 100).toFixed(1)}%` }} />
                </div>
                <span className="shrink-0 text-xs text-navy-600">{pct.toFixed(1)}%</span>
              </div>
              {trendLabel && (
                <p className={`mt-1.5 text-xs ${trendColor}`}>{trendLabel}</p>
              )}
              <p className="mt-0.5 text-xs text-navy-400">{t.samples.length} sample{t.samples.length !== 1 ? 's' : ''}</p>
            </div>
          );
        })}
      </div>
    </Card>
  );
}

// Maps SMART attribute names to whether a positive delta is critical (true) or warning (false).
const CRITICAL_SMART_ATTRS = {
  Offline_Uncorrectable: true,
  Reported_Uncorrect: true,
  Reallocated_Sector_Ct: false,
  Current_Pending_Sector: false,
};

function SmartTrendsPanel({ trends }) {
  const devices = Object.values(trends).filter(
    (d) => d.attrs && Object.keys(d.attrs).length > 0
  );
  if (devices.length === 0) return null;

  return (
    <Card title="SMART trends" subtitle="30-day attribute history per device — pre-failure indicator tracking">
      <div className="space-y-4">
        {devices.map((dev) => {
          const attrList = Object.values(dev.attrs).filter((a) => a.samples && a.samples.length > 0);
          if (attrList.length === 0) return null;

          return (
            <div key={dev.device}>
              <p className="mb-2 text-xs font-semibold uppercase tracking-wide text-navy-500">{dev.device}</p>
              <div className="overflow-x-auto rounded-lg border border-navy-200">
                <table className="w-full text-xs">
                  <thead>
                    <tr className="border-b border-navy-200 text-left text-navy-500">
                      <th className="px-3 py-2 font-medium">Attribute</th>
                      <th className="px-3 py-2 font-medium text-right">Current</th>
                      <th className="px-3 py-2 font-medium text-right">Δ baseline</th>
                      <th className="px-3 py-2 font-medium text-right">Trend/day</th>
                      <th className="px-3 py-2 font-medium text-right">Samples</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-navy-100">
                    {attrList.map((attr) => {
                      const current = attr.samples[attr.samples.length - 1]?.value ?? 0;
                      const delta = attr.delta ?? 0;
                      const slope = attr.slopePerDay ?? 0;
                      const isCriticalAttr = CRITICAL_SMART_ATTRS[attr.name];
                      const isWear = attr.name === 'NVMe_Wear_Pct' || attr.name === 'Wear';

                      let deltaColor = 'text-navy-500';
                      if (isWear) {
                        deltaColor = current >= 90 ? 'text-rose-700' : current >= 70 ? 'text-amber-700' : 'text-navy-600';
                      } else if (delta > 0) {
                        const isCritical = isCriticalAttr || slope > 1.0;
                        deltaColor = isCritical ? 'text-rose-700' : 'text-amber-700';
                      }

                      const slopeLabel = slope > 0 ? `+${slope.toFixed(2)}` : slope.toFixed(2);
                      const slopeColor = slope > 1.0 ? 'text-rose-700' : slope > 0.1 ? 'text-amber-700' : 'text-navy-500';

                      return (
                        <tr key={attr.name} className="hover:bg-navy-50">
                          <td className="px-3 py-2 font-medium text-navy-700">{attr.name}</td>
                          <td className="px-3 py-2 text-right text-navy-600">
                            {isWear ? `${current}%` : current}
                          </td>
                          <td className={`px-3 py-2 text-right font-medium ${deltaColor}`}>
                            {delta > 0 ? `+${delta}` : delta === 0 ? '—' : delta}
                          </td>
                          <td className={`px-3 py-2 text-right ${slopeColor}`}>
                            {slope !== 0 ? slopeLabel : '—'}
                          </td>
                          <td className="px-3 py-2 text-right text-navy-400">{attr.samples.length}</td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            </div>
          );
        })}
      </div>
    </Card>
  );
}

function logLineColor(line) {
  if (/\b(ERROR|FATAL|PANIC)\b/i.test(line)) return 'text-red-400';
  if (/\b(WARN(?:ING)?)\b/i.test(line)) return 'text-yellow-300';
  return 'text-emerald-300';
}

function AgentLogTab({ agentId }) {
  const [logData, setLogData] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const bottomRef = useRef(null);
  const didInitialScroll = useRef(false);

  const load = () => {
    fetchAgentLog(agentId)
      .then((data) => { setLogData(data); setLoading(false); })
      .catch((err) => { setError(err); setLoading(false); });
  };

  useEffect(() => {
    load();
  }, [agentId]); // eslint-disable-line react-hooks/exhaustive-deps

  // Scroll to bottom once on initial load.
  useEffect(() => {
    if (logData && !didInitialScroll.current) {
      didInitialScroll.current = true;
      bottomRef.current?.scrollIntoView({ behavior: 'instant' });
    }
  }, [logData]);

  const refresh = () => {
    setLoading(true);
    load();
  };

  return (
    <Card
      title="Agent log"
      subtitle={
        logData?.truncated
          ? 'Showing last 1 MB of log file — oldest entries omitted'
          : 'Complete log file contents'
      }
    >
      <div className="mb-3 flex items-center justify-between">
        {logData?.note && (
          <p className="text-xs text-navy-500 italic">{logData.note}</p>
        )}
        {!logData?.note && <span />}
        <button
          onClick={refresh}
          disabled={loading}
          className="inline-flex items-center gap-1.5 rounded-lg border border-navy-300 px-3 py-1.5 text-xs font-medium text-navy-600 hover:border-navy-400 hover:text-navy-800 disabled:opacity-50 transition-colors"
        >
          {loading ? 'Loading...' : 'Refresh'}
        </button>
      </div>
      {error && <AsyncState error={error} />}
      {!error && (
        <div className="relative h-[36rem] overflow-y-auto rounded-lg border border-navy-200 bg-navy-950 p-3">
          <pre className="whitespace-pre-wrap font-mono text-xs leading-relaxed">
            {logData?.content
              ? logData.content.split('\n').map((line, i) => (
                  <span key={i} className={logLineColor(line)}>
                    {line}{'\n'}
                  </span>
                ))
              : (loading ? '' : <span className="text-emerald-300">(no log content)</span>)
            }
          </pre>
          <div ref={bottomRef} />
        </div>
      )}
    </Card>
  );
}

function AgentCardTab({ agent }) {
  const { data: card, loading, error } = useApi(() => fetchAgentCard(agent.id), [agent.id]);

  if (loading) return <AsyncState loading loadingLabel="Loading agent card..." />;
  if (error) return <AsyncState error={error} />;
  if (!card) return null;

  const interfaces = card.supportedInterfaces ?? [];

  return (
    <div className="space-y-4">
      {/* Identity */}
      <Card title="Identity" subtitle={`A2A agent card — ${agent.hostname}/.well-known/agent-card.json`}>
        <dl className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div>
            <dt className="text-xs font-semibold uppercase tracking-wide text-navy-500">Name</dt>
            <dd className="mt-1 text-sm font-medium text-navy-900">{card.name}</dd>
          </div>
          <div>
            <dt className="text-xs font-semibold uppercase tracking-wide text-navy-500">Build version</dt>
            <dd className="mt-1 font-mono text-sm text-navy-900">{card.version ?? 'unknown'}</dd>
          </div>
          {agent.buildTime && (
            <div>
              <dt className="text-xs font-semibold uppercase tracking-wide text-navy-500">Build time</dt>
              <dd className="mt-1 font-mono text-sm text-navy-900">{agent.buildTime}</dd>
            </div>
          )}
          {card.description && (
            <div className="sm:col-span-2">
              <dt className="text-xs font-semibold uppercase tracking-wide text-navy-500">Description</dt>
              <dd className="mt-1 text-sm text-navy-700">{card.description}</dd>
            </div>
          )}
        </dl>
      </Card>

      {/* Endpoint interfaces */}
      {interfaces.length > 0 && (
        <Card title="Endpoint" subtitle="Transport protocol and connection URL">
          <div className="space-y-2">
            {interfaces.map((iface, i) => (
              <div key={i} className="flex flex-wrap items-center gap-3 rounded-lg bg-navy-50 px-3 py-2.5">
                <span className="rounded-md bg-navy-200 px-2 py-0.5 font-mono text-xs font-medium text-navy-700">
                  {iface.protocolBinding ?? 'JSONRPC'}
                </span>
                {iface.protocolVersion && (
                  <span className="rounded-md bg-navy-100 px-2 py-0.5 text-xs text-navy-600">
                    A2A {iface.protocolVersion}
                  </span>
                )}
                <span className="font-mono text-sm text-navy-800 break-all">{iface.url}</span>
              </div>
            ))}
          </div>
        </Card>
      )}

      {/* Skills */}
      {card.skills?.length > 0 && (
        <Card
          title={`Skills (${card.skills.length})`}
          subtitle="Capabilities registered with this agent"
        >
          <div className="space-y-2">
            {card.skills.map((skill) => (
              <CollapsibleSection key={skill.id} title={skill.name} subtitle={skill.description}>
                <div className="space-y-3">
                  <div>
                    <p className="text-xs font-semibold uppercase tracking-wide text-navy-500">ID</p>
                    <p className="mt-0.5 font-mono text-xs text-navy-700">{skill.id}</p>
                  </div>
                  {skill.description && (
                    <div>
                      <p className="text-xs font-semibold uppercase tracking-wide text-navy-500">Description</p>
                      <p className="mt-0.5 text-xs text-navy-700 leading-relaxed">{skill.description}</p>
                    </div>
                  )}
                  {skill.tags?.length > 0 && (
                    <div className="flex flex-wrap gap-1.5">
                      {skill.tags.map((tag) => (
                        <span key={tag} className="rounded-full bg-navy-100 px-2.5 py-0.5 text-xs text-navy-600">
                          {tag}
                        </span>
                      ))}
                    </div>
                  )}
                </div>
              </CollapsibleSection>
            ))}
          </div>
        </Card>
      )}

      {/* Security */}
      {card.securitySchemes && Object.keys(card.securitySchemes).length > 0 && (
        <Card title="Security" subtitle="Authentication schemes declared by this agent">
          <div className="space-y-2">
            {Object.entries(card.securitySchemes).map(([name, scheme]) => {
              const schemeType = scheme?.scheme ?? scheme?.type ?? (typeof scheme === 'string' ? scheme : null);
              return (
                <div key={name} className="flex items-center gap-3 rounded-lg border border-navy-200 px-3 py-2.5">
                  <span className="shrink-0 rounded-md bg-amber-100 px-2 py-0.5 text-xs font-semibold text-amber-800">
                    {name}
                  </span>
                  {schemeType && (
                    <span className="font-mono text-xs text-navy-600">{schemeType}</span>
                  )}
                </div>
              );
            })}
          </div>
        </Card>
      )}
    </div>
  );
}

function AgentAdminTab({ agent }) {
  const { data: memData, loading: memLoading, error: memError } = useApi(
    () => fetchAgentMemory(agent.id), [agent.id]
  );
  const [clearState, setClearState] = useState('idle');
  const [errorMsg, setErrorMsg] = useState('');

  const doClear = async () => {
    setClearState('working');
    try {
      await clearAgentMemory(agent.id);
      setClearState('done');
      setTimeout(() => setClearState('idle'), 3000);
    } catch (err) {
      setErrorMsg(err.message);
      setClearState('error');
      setTimeout(() => setClearState('idle'), 4000);
    }
  };

  const domains = Object.entries(memData?.domains ?? {});
  const attrs = Object.entries(memData?.attrs ?? {});

  return (
    <div className="space-y-4">
      {/* ── Memory ── */}
      <Card title="Agent memory" subtitle="Cached snapshots and attributes stored by this agent's skills">
        {memLoading && <AsyncState loading loadingLabel="Loading memory..." />}
        {memError && <AsyncState error={memError} />}
        {!memLoading && !memError && (
          <div className="space-y-2">
            {domains.length === 0 && attrs.length === 0 ? (
              <p className="text-sm text-navy-500">No memory recorded yet.</p>
            ) : (
              <>
                {domains.map(([name, value]) => (
                  <CollapsibleSection
                    key={name}
                    title={name}
                    subtitle={Array.isArray(value) ? `${value.length} entr${value.length === 1 ? 'y' : 'ies'}` : 'domain snapshot'}
                  >
                    <pre className="overflow-x-auto rounded-md bg-navy-50 p-3 text-xs text-navy-700">
                      {JSON.stringify(value, null, 2)}
                    </pre>
                  </CollapsibleSection>
                ))}
                {attrs.length > 0 && (
                  <CollapsibleSection
                    title="Attributes"
                    subtitle={`${attrs.length} key${attrs.length === 1 ? '' : 's'} · skill health, last-run state`}
                  >
                    <div className="space-y-2">
                      {attrs.map(([key, value]) => (
                        <div key={key}>
                          <p className="mb-1 text-xs font-semibold uppercase tracking-wide text-navy-500">{key}</p>
                          <pre className="overflow-x-auto rounded-md bg-navy-50 p-3 text-xs text-navy-700">
                            {JSON.stringify(value, null, 2)}
                          </pre>
                        </div>
                      ))}
                    </div>
                  </CollapsibleSection>
                )}
              </>
            )}
          </div>
        )}
      </Card>

      {/* ── Danger zone ── */}
      <Card title="Danger zone" subtitle="Destructive actions for this agent">
        <div className="flex items-center justify-between rounded-lg border border-navy-200 p-4">
          <div>
            <p className="text-sm font-medium text-navy-900">Clear agent memory</p>
            <p className="mt-0.5 text-xs text-navy-500">
              Removes all cached timeline snapshots, skill state, and attrs. The agent rebuilds memory on its next run cycle.
            </p>
          </div>
          <div className="shrink-0 pl-4">
            {clearState === 'idle' && (
              <button
                onClick={() => setClearState('confirm')}
                className="inline-flex items-center gap-1.5 rounded-lg border border-rose-300 px-3 py-1.5 text-xs font-semibold text-rose-700 transition-colors hover:border-rose-600 hover:bg-rose-50"
              >
                <TrashIcon className="h-3.5 w-3.5" />
                Clear memory
              </button>
            )}
            {clearState === 'confirm' && (
              <div className="flex items-center gap-2">
                <span className="text-xs text-navy-600">Are you sure?</span>
                <button
                  onClick={doClear}
                  className="inline-flex items-center gap-1.5 rounded-lg bg-rose-600 px-3 py-1.5 text-xs font-semibold text-white hover:bg-rose-700"
                >
                  Yes, clear
                </button>
                <button
                  onClick={() => setClearState('idle')}
                  className="rounded-lg border border-navy-300 px-3 py-1.5 text-xs text-navy-600 hover:text-navy-900"
                >
                  Cancel
                </button>
              </div>
            )}
            {clearState === 'working' && <span className="text-xs text-navy-500">Clearing...</span>}
            {clearState === 'done' && <span className="text-xs text-emerald-700">Memory cleared</span>}
            {clearState === 'error' && <span className="text-xs text-rose-700">{errorMsg}</span>}
          </div>
        </div>
      </Card>
    </div>
  );
}
