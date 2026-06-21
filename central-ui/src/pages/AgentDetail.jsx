import { useState, useEffect, useRef } from 'react';
import { Link, useParams } from 'react-router-dom';
import Badge from '../components/Badge';
import Card from '../components/Card';
import TimelineEntry from '../components/TimelineEntry';
import AsyncState from '../components/AsyncState';
import { ChatIcon, CheckIcon, XIcon, TrashIcon, ChevronRightIcon } from '../components/icons';
import { fetchAgent, fetchAgentMemory, sendAgentMessage, clearAgentMemory, decideApproval } from '../api/client';
import { useApi } from '../hooks/useApi';
import { useFleetSocket } from '../hooks/useFleetSocket';
import { useChatHistory } from '../hooks/useChatHistory';
import { relativeTime } from '../utils/time';

const TABS = ['Interact', 'Recommendations & Approvals', 'Activity', 'Admin'];

export default function AgentDetail() {
  const { id } = useParams();
  const { data: agent, loading, error } = useApi(() => fetchAgent(id), [id]);
  const [tab, setTab] = useState('Interact');
  const [approvalState, setApprovalState] = useState({});

  if (loading) {
    return <AsyncState loading loadingLabel="Loading agent..." />;
  }

  if (error?.message?.includes('404') || (!loading && !error && !agent)) {
    return (
      <div className="space-y-4">
        <p className="text-sm text-slate-400">Agent not found.</p>
        <Link to="/agents" className="text-sm font-medium text-indigo-400 hover:text-indigo-300">&larr; Back to agents</Link>
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
        <Link to="/agents" className="text-xs font-medium text-slate-500 hover:text-slate-300">&larr; Agents</Link>
        <div className="mt-2 flex flex-wrap items-center justify-between gap-4">
          <div>
            <div className="flex items-center gap-3">
              <h1 className="text-2xl font-semibold text-slate-100">{agent.hostname}</h1>
              <Badge variant={agent.status}>{agent.statusLabel}</Badge>
            </div>
            <p className="mt-1 text-sm text-slate-500">
              {agent.role} &middot; {agent.os} &middot; last polled {relativeTime(agent.lastPoll)}
            </p>
          </div>
          <div className="flex gap-2">
            {agent.tags.map((t) => (
              <span key={t} className="rounded-full bg-slate-800 px-2.5 py-1 text-xs text-slate-400">{t}</span>
            ))}
          </div>
        </div>
        <div className="mt-3 rounded-md bg-slate-900/60 px-3 py-2 text-sm text-slate-300 ring-1 ring-inset ring-slate-800">
          <span className="text-xs font-medium uppercase tracking-wide text-slate-500">
            {agent.currentTask ? 'Currently working on' : 'Status'}
          </span>
          <p className="mt-0.5">{agent.currentTask ?? agent.statusDescription}</p>
        </div>
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

      {tab === 'Admin' && <AgentAdminTab agent={agent} />}
    </div>
  );
}

function RecommendationsTab({ agent, resolveStatus, decide }) {
  const approvals = agent.timeline.filter((t) => t.type === 'approval');
  const recommendations = agent.timeline.filter((t) => t.type === 'recommendation');

  return (
    <div className="space-y-4">
      <Card title="Awaiting your decision" subtitle="The agent will act once you approve or reject these">
        {approvals.length === 0 ? (
          <p className="text-sm text-slate-500">This agent has no actions waiting on approval.</p>
        ) : (
          <div className="space-y-4">
            {approvals.map((entry) => {
              const status = resolveStatus(entry);
              return (
                <div key={entry.id} className="rounded-lg border border-slate-800 p-4">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant={entry.risk}>{entry.risk} risk</Badge>
                    <Badge variant={status}>{status}</Badge>
                    <span className="text-xs text-slate-600">{relativeTime(entry.time)}</span>
                  </div>
                  <p className="mt-2 text-sm font-medium text-slate-200">{entry.title}</p>
                  <p className="mt-1 text-sm text-slate-500">{entry.detail}</p>
                  <p className="mt-2 rounded-md bg-slate-800/70 px-3 py-2 font-mono text-xs text-slate-400">
                    {entry.proposedAction}
                  </p>
                  {status === 'pending' ? (
                    <div className="mt-3 flex gap-2">
                      <button
                        onClick={() => decide(entry.id, 'approved')}
                        className="inline-flex items-center gap-1.5 rounded-lg bg-emerald-500 px-3 py-1.5 text-xs font-semibold text-white hover:bg-emerald-400"
                      >
                        <CheckIcon className="h-3.5 w-3.5" /> Approve
                      </button>
                      <button
                        onClick={() => decide(entry.id, 'rejected')}
                        className="inline-flex items-center gap-1.5 rounded-lg border border-slate-700 px-3 py-1.5 text-xs font-semibold text-slate-300 hover:border-rose-500/50 hover:text-rose-300"
                      >
                        <XIcon className="h-3.5 w-3.5" /> Reject
                      </button>
                    </div>
                  ) : (
                    <p className="mt-3 text-xs text-slate-500">
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

      <Card title="Other recommendations" subtitle="Suggestions that don't require a specific action right now">
        {recommendations.length === 0 ? (
          <p className="text-sm text-slate-500">No open recommendations.</p>
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
  const { messages, addMessage } = useChatHistory(agent.id);
  const [input, setInput] = useState('');
  const [sending, setSending] = useState(false);
  // Per-approval decision state: id -> { loading, decision, error }
  const [approvalDecisions, setApprovalDecisions] = useState({});
  const bottomRef = useRef(null);

  // IDs of approvals that existed when this tab mounted — we only inject
  // approvals that appear after the user sends a message.
  const knownApprovalIds = useRef(
    new Set(agent.timeline.filter((e) => e.type === 'approval').map((e) => e.id))
  );

  // Watch live approval count for this agent from the WebSocket.
  const { agents: wsAgents } = useFleetSocket();
  const wsApprovalCount = wsAgents?.find((a) => a.id === agent.id)?.pendingApprovalCount ?? 0;

  // Greet on first visit only.
  useEffect(() => {
    if (messages.length === 0) {
      addMessage({
        role: 'agent',
        text: `Hi, I'm the agent for ${agent.hostname}. Ask me about what I've seen, what I'm doing, or why I've made a recommendation.`,
      });
    }
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

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

  const send = async (text) => {
    const value = (text ?? input).trim();
    if (!value || sending) return;
    addMessage({ role: 'user', text: value });
    setInput('');
    setSending(true);
    try {
      const { reply } = await sendAgentMessage(agent.id, value);
      addMessage({ role: 'agent', text: reply || '(no response)' });
    } catch (err) {
      addMessage({ role: 'agent', text: `Couldn't reach the agent: ${err.message}` });
    } finally {
      setSending(false);
    }
  };

  const resolveApproval = async (entry, decision) => {
    setApprovalDecisions((prev) => ({ ...prev, [entry.id]: { loading: true } }));
    try {
      await decideApproval(entry.id, decision, agent.id);
      setApprovalDecisions((prev) => ({ ...prev, [entry.id]: { decision } }));
    } catch (err) {
      setApprovalDecisions((prev) => ({ ...prev, [entry.id]: { error: err.message } }));
    }
  };

  return (
    <Card title={`Talk to the ${agent.hostname} agent`} subtitle="Sends a message via the A2A JSON-RPC API">
      <div className="flex h-[32rem] flex-col">
        <div className="flex-1 space-y-3 overflow-y-auto pr-1">
          {messages.map((m, i) => {
            if (m.role === 'approval') {
              const state = approvalDecisions[m.entry.id];
              const decided = state?.decision;
              return (
                <div key={i} className="flex justify-start">
                  <div className="w-full max-w-[92%] rounded-xl border border-amber-800/50 bg-amber-950/20 p-3 text-sm">
                    <div className="mb-2 flex items-center gap-2">
                      <span className="text-xs font-semibold uppercase tracking-wide text-amber-400">
                        Approval Required
                      </span>
                      <Badge variant={m.entry.risk}>{m.entry.risk} risk</Badge>
                    </div>
                    <p className="font-medium text-slate-200">{m.entry.title}</p>
                    <p className="mt-1 text-xs text-slate-400">{m.entry.detail}</p>
                    {m.entry.proposedAction && (
                      <p className="mt-2 rounded bg-slate-900 px-2 py-1.5 font-mono text-xs text-slate-400">
                        {m.entry.proposedAction}
                      </p>
                    )}
                    {decided ? (
                      <p className={`mt-2 text-xs font-medium ${decided === 'approved' ? 'text-emerald-400' : 'text-rose-400'}`}>
                        {decided === 'approved'
                          ? '✓ Approved — agent will proceed'
                          : '✗ Rejected — agent will not proceed'}
                      </p>
                    ) : state?.error ? (
                      <p className="mt-2 text-xs text-rose-400">{state.error}</p>
                    ) : (
                      <div className="mt-3 flex gap-2">
                        <button
                          onClick={() => resolveApproval(m.entry, 'approved')}
                          disabled={state?.loading}
                          className="inline-flex items-center gap-1.5 rounded-lg bg-emerald-500 px-3 py-1.5 text-xs font-semibold text-white hover:bg-emerald-400 disabled:cursor-not-allowed disabled:opacity-50"
                        >
                          <CheckIcon className="h-3.5 w-3.5" /> Approve
                        </button>
                        <button
                          onClick={() => resolveApproval(m.entry, 'rejected')}
                          disabled={state?.loading}
                          className="inline-flex items-center gap-1.5 rounded-lg border border-slate-700 px-3 py-1.5 text-xs font-semibold text-slate-300 hover:border-rose-500/50 hover:text-rose-300 disabled:cursor-not-allowed disabled:opacity-50"
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
                    m.role === 'user' ? 'bg-indigo-500 text-white' : 'bg-slate-800 text-slate-200'
                  }`}
                >
                  {m.text}
                </div>
              </div>
            );
          })}
          {sending && (
            <div className="flex justify-start">
              <div className="max-w-[80%] rounded-xl bg-slate-800 px-3.5 py-2.5 text-sm text-slate-400">
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
              className="rounded-full border border-slate-700 px-3 py-1 text-xs text-slate-400 hover:border-indigo-500/50 hover:text-indigo-300 disabled:opacity-50"
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
            placeholder="Ask this agent something..."
            disabled={sending}
            className="flex-1 rounded-lg border border-slate-800 bg-slate-900/60 px-3 py-2 text-sm text-slate-200 placeholder:text-slate-500 focus:border-indigo-500 focus:outline-none disabled:opacity-50"
          />
          <button
            type="submit"
            disabled={sending}
            className="rounded-lg bg-indigo-500 px-4 py-2 text-sm font-semibold text-white hover:bg-indigo-400 disabled:opacity-50"
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
    <div className="rounded-lg border border-slate-800">
      <button
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-center gap-3 px-4 py-3 text-left hover:bg-slate-800/40 transition-colors rounded-lg"
      >
        <ChevronRightIcon
          className={`h-4 w-4 shrink-0 text-slate-500 transition-transform duration-150 ${open ? 'rotate-90' : ''}`}
        />
        <div className="min-w-0 flex-1">
          <p className="text-sm font-medium text-slate-200">{title}</p>
          {subtitle && <p className="mt-0.5 text-xs text-slate-500 truncate">{subtitle}</p>}
        </div>
      </button>
      {open && (
        <div className="border-t border-slate-800 px-4 pb-4 pt-3">
          {children}
        </div>
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
              <p className="text-sm text-slate-500">No memory recorded yet.</p>
            ) : (
              <>
                {domains.map(([name, value]) => (
                  <CollapsibleSection
                    key={name}
                    title={name}
                    subtitle={Array.isArray(value) ? `${value.length} entr${value.length === 1 ? 'y' : 'ies'}` : 'domain snapshot'}
                  >
                    <pre className="overflow-x-auto rounded-md bg-slate-950/60 p-3 text-xs text-slate-300">
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
                          <p className="mb-1 text-xs font-semibold uppercase tracking-wide text-slate-500">{key}</p>
                          <pre className="overflow-x-auto rounded-md bg-slate-950/60 p-3 text-xs text-slate-300">
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
        <div className="flex items-center justify-between rounded-lg border border-slate-800 p-4">
          <div>
            <p className="text-sm font-medium text-slate-200">Clear agent memory</p>
            <p className="mt-0.5 text-xs text-slate-500">
              Removes all cached timeline snapshots, skill state, and attrs. The agent rebuilds memory on its next run cycle.
            </p>
          </div>
          <div className="shrink-0 pl-4">
            {clearState === 'idle' && (
              <button
                onClick={() => setClearState('confirm')}
                className="inline-flex items-center gap-1.5 rounded-lg border border-rose-800/60 px-3 py-1.5 text-xs font-semibold text-rose-400 transition-colors hover:border-rose-600 hover:bg-rose-600/10"
              >
                <TrashIcon className="h-3.5 w-3.5" />
                Clear memory
              </button>
            )}
            {clearState === 'confirm' && (
              <div className="flex items-center gap-2">
                <span className="text-xs text-slate-400">Are you sure?</span>
                <button
                  onClick={doClear}
                  className="inline-flex items-center gap-1.5 rounded-lg bg-rose-600 px-3 py-1.5 text-xs font-semibold text-white hover:bg-rose-500"
                >
                  Yes, clear
                </button>
                <button
                  onClick={() => setClearState('idle')}
                  className="rounded-lg border border-slate-700 px-3 py-1.5 text-xs text-slate-400 hover:text-slate-200"
                >
                  Cancel
                </button>
              </div>
            )}
            {clearState === 'working' && <span className="text-xs text-slate-500">Clearing...</span>}
            {clearState === 'done' && <span className="text-xs text-emerald-400">Memory cleared</span>}
            {clearState === 'error' && <span className="text-xs text-rose-400">{errorMsg}</span>}
          </div>
        </div>
      </Card>
    </div>
  );
}
