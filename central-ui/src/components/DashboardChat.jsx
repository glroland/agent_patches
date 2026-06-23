import { useState, useEffect, useRef } from 'react';
import { Link } from 'react-router-dom';
import { ChatIcon, CheckIcon, XIcon } from './icons';
import { sendCentralChat, fetchAgent, decideApproval } from '../api/client';
import { useChatHistory } from '../hooks/useChatHistory';
import { useFleetSocket } from '../hooks/useFleetSocket';
import Badge from './Badge';

const FLEET_CHAT_ID = '__fleet__';

const SUGGESTIONS = [
  'What needs attention right now?',
  'Which agents haven\'t been patched recently?',
  'Any disk issues across the fleet?',
  'Summarize recent critical events',
];

// Build the history array the backend expects from our message list.
// Combines assistant + adjacent agent replies into a single assistant turn.
function buildHistory(messages) {
  const result = [];
  for (let i = 0; i < messages.length; i++) {
    const m = messages[i];
    if (m.role === 'user') {
      result.push({ role: 'user', text: m.text });
    } else if (m.role === 'assistant') {
      let text = m.text;
      if (messages[i + 1]?.role === 'agent') {
        text += `\n\n[${messages[i + 1].hostname} replied]: ${messages[i + 1].text}`;
        i++;
      }
      result.push({ role: 'assistant', text });
    }
    // skip 'approval' messages — they're visible in fleet state anyway
  }
  return result;
}

export default function DashboardChat() {
  const { messages, addMessage } = useChatHistory(FLEET_CHAT_ID);
  const [input, setInput] = useState('');
  const [sending, setSending] = useState(false);
  // Track which agent was last routed to so we can watch for its approvals.
  const [routedAgentId, setRoutedAgentId] = useState(null);
  const [approvalDecisions, setApprovalDecisions] = useState({});
  const knownApprovalIds = useRef(new Set());
  const bottomRef = useRef(null);

  const { agents: wsAgents } = useFleetSocket();
  const routedApprovalCount =
    wsAgents?.find((a) => a.id === routedAgentId)?.pendingApprovalCount ?? 0;

  // Auto-scroll to bottom on new messages.
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages, sending]);

  // Watch the WebSocket approval count for the last routed agent.
  // Fires on every count change and injects new pending approvals inline.
  useEffect(() => {
    if (!routedAgentId) return;
    fetchAgent(routedAgentId)
      .then((fullAgent) => {
        const newApprovals = (fullAgent?.timeline ?? []).filter(
          (e) =>
            e.type === 'approval' &&
            e.status === 'pending' &&
            !knownApprovalIds.current.has(e.id)
        );
        newApprovals.forEach((entry) => {
          knownApprovalIds.current.add(entry.id);
          addMessage({ role: 'approval', entry, agentId: routedAgentId });
        });
      })
      .catch(() => {});
  }, [routedAgentId, routedApprovalCount]); // eslint-disable-line react-hooks/exhaustive-deps

  const send = async (text) => {
    const value = (text ?? input).trim();
    if (!value || sending) return;
    setInput('');
    setSending(true);

    addMessage({ role: 'user', text: value });
    const history = buildHistory(messages);

    try {
      const { reply, routedTo, agentReply } = await sendCentralChat(value, history);
      addMessage({ role: 'assistant', text: reply, routedTo });
      if (agentReply) {
        addMessage({ role: 'agent', text: agentReply, agentId: routedTo.id, hostname: routedTo.hostname });
      }
      if (routedTo) {
        setRoutedAgentId(routedTo.id);
      }
    } catch (err) {
      addMessage({ role: 'assistant', text: `Error: ${err.message}` });
    } finally {
      setSending(false);
    }
  };

  const resolveApproval = async (entry, agentId, decision) => {
    setApprovalDecisions((prev) => ({ ...prev, [entry.id]: { loading: true } }));
    try {
      await decideApproval(entry.id, decision, agentId);
      setApprovalDecisions((prev) => ({ ...prev, [entry.id]: { decision } }));
    } catch (err) {
      setApprovalDecisions((prev) => ({ ...prev, [entry.id]: { error: err.message } }));
    }
  };

  return (
    <div className="rounded-xl border border-slate-800 bg-slate-900/30">
      <div className="flex items-center justify-between border-b border-slate-800 px-4 py-3">
        <div>
          <p className="text-sm font-semibold text-slate-200">Fleet Chat</p>
          <p className="text-xs text-slate-500">Ask about the fleet or interact with individual agents</p>
        </div>
      </div>

      <div className="flex h-80 flex-col p-4">
        <div className="flex-1 space-y-3 overflow-y-auto pr-1">
          {messages.length === 0 && (
            <p className="pt-4 text-center text-sm text-slate-600">
              Ask about fleet status, recent issues, patch state, or a specific agent.
            </p>
          )}

          {messages.map((m, i) => {
            if (m.role === 'user') {
              return (
                <div key={i} className="flex justify-end">
                  <div className="max-w-[80%] rounded-xl bg-indigo-500 px-3.5 py-2.5 text-sm text-white whitespace-pre-wrap">
                    {m.text}
                  </div>
                </div>
              );
            }

            if (m.role === 'assistant') {
              return (
                <div key={i} className="flex justify-start">
                  <div className="max-w-[80%] space-y-1.5">
                    <p className="pl-1 text-xs font-medium text-slate-600">Fleet AI</p>
                    <div className="rounded-xl bg-slate-800 px-3.5 py-2.5 text-sm text-slate-200 whitespace-pre-wrap">
                      {m.text}
                    </div>
                    {m.routedTo && (
                      <div className="flex items-center gap-1.5 pl-1">
                        <span className="text-xs text-slate-600">→ routed to</span>
                        <Link
                          to={`/agents/${m.routedTo.id}`}
                          className="text-xs font-medium text-indigo-400 hover:text-indigo-300"
                        >
                          {m.routedTo.id}
                        </Link>
                      </div>
                    )}
                  </div>
                </div>
              );
            }

            if (m.role === 'agent') {
              const shortHost = m.hostname?.split('.')[0] ?? m.agentId;
              return (
                <div key={i} className="flex justify-start pl-4">
                  <div className="max-w-[80%] space-y-1.5">
                    <div className="flex items-center gap-1.5 pl-1">
                      <span className="rounded-full bg-emerald-500/15 px-2 py-0.5 text-xs font-semibold text-emerald-400">
                        {shortHost}
                      </span>
                    </div>
                    <div className="rounded-xl border border-emerald-500/20 bg-emerald-950/20 px-3.5 py-2.5 text-sm text-slate-200 whitespace-pre-wrap">
                      {m.text}
                    </div>
                  </div>
                </div>
              );
            }

            if (m.role === 'approval') {
              const state = approvalDecisions[m.entry.id];
              const decided = state?.decision;
              const shortHost = m.entry.hostname?.split('.')[0] ?? m.agentId;
              return (
                <div key={i} className="flex justify-start">
                  <div className="w-full max-w-[92%] rounded-xl border border-amber-800/50 bg-amber-950/20 p-3 text-sm">
                    <div className="mb-2 flex items-center gap-2">
                      <span className="text-xs font-semibold uppercase tracking-wide text-amber-400">
                        Approval Required
                      </span>
                      <Badge variant={m.entry.risk}>{m.entry.risk} risk</Badge>
                      <span className="text-xs text-slate-600">from {shortHost}</span>
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
                        {decided === 'approved' ? '✓ Approved — agent will proceed' : '✗ Rejected — agent will not proceed'}
                      </p>
                    ) : state?.error ? (
                      <p className="mt-2 text-xs text-rose-400">{state.error}</p>
                    ) : (
                      <div className="mt-3 flex gap-2">
                        <button
                          onClick={() => resolveApproval(m.entry, m.agentId, 'approved')}
                          disabled={state?.loading}
                          className="inline-flex items-center gap-1.5 rounded-lg bg-emerald-500 px-3 py-1.5 text-xs font-semibold text-white hover:bg-emerald-400 disabled:cursor-not-allowed disabled:opacity-50"
                        >
                          <CheckIcon className="h-3.5 w-3.5" /> Approve
                        </button>
                        <button
                          onClick={() => resolveApproval(m.entry, m.agentId, 'rejected')}
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

            return null;
          })}

          {sending && (
            <div className="flex justify-start">
              <div className="max-w-[80%] space-y-1.5">
                <p className="pl-1 text-xs font-medium text-slate-600">Fleet AI</p>
                <div className="rounded-xl bg-slate-800 px-3.5 py-2.5 text-sm text-slate-400">
                  Thinking…
                </div>
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
          <ChatIcon className="h-4 w-4 shrink-0 text-slate-500" />
          <input
            value={input}
            onChange={(e) => setInput(e.target.value)}
            placeholder="Ask about the fleet or a specific agent…"
            disabled={sending}
            className="flex-1 rounded-lg border border-slate-800 bg-slate-900/60 px-3 py-2 text-sm text-slate-200 placeholder:text-slate-500 focus:border-indigo-500 focus:outline-none disabled:opacity-50"
          />
          <button
            type="submit"
            disabled={sending || !input.trim()}
            className="rounded-lg bg-indigo-500 px-4 py-2 text-sm font-semibold text-white hover:bg-indigo-400 disabled:opacity-50"
          >
            Send
          </button>
        </form>
      </div>
    </div>
  );
}
