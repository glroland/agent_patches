import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import Badge from '../components/Badge';
import Card from '../components/Card';
import TimelineEntry from '../components/TimelineEntry';
import AsyncState from '../components/AsyncState';
import { ChatIcon, CheckIcon, XIcon } from '../components/icons';
import { fetchAgent, sendAgentMessage } from '../api/client';
import { useApi } from '../hooks/useApi';
import { relativeTime } from '../utils/time';

const TABS = ['Activity', 'Recommendations & Approvals', 'Interact'];

export default function AgentDetail() {
  const { id } = useParams();
  const { data: agent, loading, error } = useApi(() => fetchAgent(id), [id]);
  const [tab, setTab] = useState('Activity');
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
  const [messages, setMessages] = useState([
    { role: 'agent', text: `Hi, I'm the agent for ${agent.hostname}. Ask me about what I've seen, what I'm doing, or why I've made a recommendation.` },
  ]);
  const [input, setInput] = useState('');
  const [sending, setSending] = useState(false);

  const send = async (text) => {
    const value = (text ?? input).trim();
    if (!value || sending) return;
    setMessages((prev) => [...prev, { role: 'user', text: value }]);
    setInput('');
    setSending(true);
    try {
      const { reply } = await sendAgentMessage(agent.id, value);
      setMessages((prev) => [...prev, { role: 'agent', text: reply || '(no response)' }]);
    } catch (err) {
      setMessages((prev) => [...prev, { role: 'agent', text: `Couldn't reach the agent: ${err.message}` }]);
    } finally {
      setSending(false);
    }
  };

  return (
    <Card title={`Talk to the ${agent.hostname} agent`} subtitle="Sends a message via the A2A JSON-RPC API">
      <div className="flex h-96 flex-col">
        <div className="flex-1 space-y-3 overflow-y-auto pr-1">
          {messages.map((m, i) => (
            <div key={i} className={`flex ${m.role === 'user' ? 'justify-end' : 'justify-start'}`}>
              <div
                className={`max-w-[80%] rounded-xl px-3.5 py-2.5 text-sm whitespace-pre-wrap ${
                  m.role === 'user' ? 'bg-indigo-500 text-white' : 'bg-slate-800 text-slate-200'
                }`}
              >
                {m.text}
              </div>
            </div>
          ))}
          {sending && (
            <div className="flex justify-start">
              <div className="max-w-[80%] rounded-xl bg-slate-800 px-3.5 py-2.5 text-sm text-slate-400">
                Thinking...
              </div>
            </div>
          )}
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
