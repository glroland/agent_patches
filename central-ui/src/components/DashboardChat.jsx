import { useState, useEffect, useRef } from 'react';
import { Link } from 'react-router-dom';
import { ChatIcon, CheckIcon, XIcon, TrashIcon } from './icons';
import { fetchAgent, decideApproval } from '../api/client';
import { useChatHistory, FLEET_CHAT_ID } from '../hooks/useChatHistory';
import { useFleetSocket } from '../hooks/useFleetSocket';
import Badge from './Badge';

const SUGGESTIONS = [
  'What needs attention right now?',
  'Which agents haven\'t been patched recently?',
  'Any disk issues across the fleet?',
  'Summarize recent critical events',
];

export default function DashboardChat() {
  const { messages, sending, addMessage, clearChat, sendCentralMessage } = useChatHistory(FLEET_CHAT_ID);
  const [input, setInput] = useState('');
  // Track which agent was last routed to so we can watch for its approvals.
  const [routedAgentId, setRoutedAgentId] = useState(null);
  const [approvalDecisions, setApprovalDecisions] = useState({});
  const knownApprovalIds = useRef(new Set());
  const bottomRef = useRef(null);

  const { agents: wsAgents } = useFleetSocket();
  const routedApprovalCount =
    wsAgents?.find((a) => a.id === routedAgentId)?.pendingApprovalCount ?? 0;

  const prevMessageCount = useRef(0);
  useEffect(() => {
    if (messages.length > prevMessageCount.current) {
      bottomRef.current?.scrollIntoView({ behavior: 'smooth' });
    }
    prevMessageCount.current = messages.length;
  }, [messages]);

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

    const routedTo = await sendCentralMessage(value);
    if (routedTo) {
      setRoutedAgentId(routedTo.id);
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
    <div className="rounded-xl border border-navy-200 bg-white">
      <div className="flex items-center justify-between border-b border-navy-200 px-4 py-3">
        <div>
          <p className="text-sm font-semibold text-navy-900">Chat w/Patches</p>
          <p className="text-xs text-navy-500">Ask about the fleet or interact with individual agents</p>
        </div>
        {messages.length > 0 && (
          <button
            onClick={clearChat}
            disabled={sending}
            title="Clear chat history"
            className="inline-flex items-center gap-1.5 rounded-lg border border-navy-300 px-2.5 py-1.5 text-xs font-medium text-navy-600 hover:border-navy-400 hover:text-navy-800 disabled:opacity-50 transition-colors"
          >
            <TrashIcon className="h-3.5 w-3.5" /> Clear
          </button>
        )}
      </div>

      <div className="flex h-80 flex-col p-4">
        <div className="flex-1 space-y-3 overflow-y-auto pr-1">
          {messages.length === 0 && (
            <p className="pt-4 text-center text-sm text-navy-400">
              Ask about fleet status, recent issues, patch state, or a specific agent.
            </p>
          )}

          {messages.map((m, i) => {
            if (m.role === 'user') {
              return (
                <div key={i} className="flex justify-end">
                  <div className="max-w-[80%] rounded-xl bg-navy-700 px-3.5 py-2.5 text-sm text-white whitespace-pre-wrap">
                    {m.text}
                  </div>
                </div>
              );
            }

            if (m.role === 'assistant') {
              return (
                <div key={i} className="flex justify-start">
                  <div className="max-w-[80%] space-y-1.5">
                    <p className="pl-1 text-xs font-medium text-navy-400">Fleet AI</p>
                    <div className="rounded-xl bg-navy-100 px-3.5 py-2.5 text-sm text-navy-900 whitespace-pre-wrap">
                      {m.text}
                    </div>
                    {m.routedTo && (
                      <div className="flex items-center gap-1.5 pl-1">
                        <span className="text-xs text-navy-400">→ routed to</span>
                        <Link
                          to={`/agents/${m.routedTo.id}`}
                          className="text-xs font-medium text-navy-600 hover:text-navy-800"
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
                      <span className="rounded-full bg-emerald-100 px-2 py-0.5 text-xs font-semibold text-emerald-700">
                        {shortHost}
                      </span>
                    </div>
                    <div className="rounded-xl border border-emerald-200 bg-emerald-50 px-3.5 py-2.5 text-sm text-navy-900 whitespace-pre-wrap">
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
                  <div className="w-full max-w-[92%] rounded-xl border border-amber-300 bg-amber-50 p-3 text-sm">
                    <div className="mb-2 flex items-center gap-2">
                      <span className="text-xs font-semibold uppercase tracking-wide text-amber-700">
                        Approval Required
                      </span>
                      <Badge variant={m.entry.risk}>{m.entry.risk} risk</Badge>
                      <span className="text-xs text-navy-400">from {shortHost}</span>
                    </div>
                    <p className="font-medium text-navy-900">{m.entry.title}</p>
                    <p className="mt-1 text-xs text-navy-600">{m.entry.detail}</p>
                    {m.entry.proposedAction && (
                      <p className="mt-2 rounded bg-navy-50 px-2 py-1.5 font-mono text-xs text-navy-600">
                        {m.entry.proposedAction}
                      </p>
                    )}
                    {decided ? (
                      <p className={`mt-2 text-xs font-medium ${decided === 'approved' ? 'text-emerald-700' : 'text-rose-700'}`}>
                        {decided === 'approved' ? '✓ Approved — agent will proceed' : '✗ Rejected — agent will not proceed'}
                      </p>
                    ) : state?.error ? (
                      <p className="mt-2 text-xs text-rose-700">{state.error}</p>
                    ) : (
                      <div className="mt-3 flex gap-2">
                        <button
                          onClick={() => resolveApproval(m.entry, m.agentId, 'approved')}
                          disabled={state?.loading}
                          className="inline-flex items-center gap-1.5 rounded-lg bg-emerald-500 px-3 py-1.5 text-xs font-semibold text-white hover:bg-emerald-600 disabled:cursor-not-allowed disabled:opacity-50"
                        >
                          <CheckIcon className="h-3.5 w-3.5" /> Approve
                        </button>
                        <button
                          onClick={() => resolveApproval(m.entry, m.agentId, 'rejected')}
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

            return null;
          })}

          {sending && (
            <div className="flex justify-start">
              <div className="max-w-[80%] space-y-1.5">
                <p className="pl-1 text-xs font-medium text-navy-400">Fleet AI</p>
                <div className="rounded-xl bg-navy-100 px-3.5 py-2.5 text-sm text-navy-600">
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
          <ChatIcon className="h-4 w-4 shrink-0 text-navy-500" />
          <input
            value={input}
            onChange={(e) => setInput(e.target.value)}
            placeholder="Ask about the fleet or a specific agent…"
            disabled={sending}
            className="flex-1 rounded-lg border border-navy-200 bg-white px-3 py-2 text-sm text-navy-900 placeholder:text-navy-400 focus:border-navy-500 focus:outline-none disabled:opacity-50"
          />
          <button
            type="submit"
            disabled={sending || !input.trim()}
            className="rounded-lg bg-navy-700 px-4 py-2 text-sm font-semibold text-white hover:bg-navy-800 disabled:opacity-50"
          >
            Send
          </button>
        </form>
      </div>
    </div>
  );
}
