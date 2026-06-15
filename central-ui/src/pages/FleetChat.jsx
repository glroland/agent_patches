import { useState } from 'react';
import Card from '../components/Card';
import { ChatIcon } from '../components/icons';
import { broadcastMessage } from '../api/client';

const SUGGESTIONS = [
  'What are you working on right now?',
  'Summarize what you have seen recently',
  'Re-check everything now',
];

export default function FleetChat() {
  const [rounds, setRounds] = useState([]);
  const [input, setInput] = useState('');
  const [sending, setSending] = useState(false);

  const send = async (text) => {
    const value = (text ?? input).trim();
    if (!value || sending) return;
    setInput('');
    setSending(true);

    const round = { message: value, results: null, error: null };
    setRounds((prev) => [...prev, round]);

    try {
      const { results } = await broadcastMessage(value);
      setRounds((prev) => prev.map((r) => (r === round ? { ...r, results } : r)));
    } catch (err) {
      setRounds((prev) => prev.map((r) => (r === round ? { ...r, results: [], error: err.message } : r)));
    } finally {
      setSending(false);
    }
  };

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold text-slate-100">Fleet Chat</h1>
        <p className="mt-1 text-sm text-slate-500">
          Send a message to every agent in the fleet at once and compare their replies.
        </p>
      </div>

      <Card title="Broadcast" subtitle="Sends a message to all agents via the A2A JSON-RPC API">
        <div className="space-y-5">
          {rounds.length === 0 && (
            <p className="text-sm text-slate-500">Ask all agents something to get started.</p>
          )}

          {rounds.map((round, i) => (
            <div key={i} className="space-y-3">
              <div className="flex justify-end">
                <div className="max-w-[80%] rounded-xl bg-indigo-500 px-3.5 py-2.5 text-sm text-white whitespace-pre-wrap">
                  {round.message}
                </div>
              </div>

              {round.error && (
                <p className="text-sm text-rose-400">Broadcast failed: {round.error}</p>
              )}

              {round.results === null && !round.error && (
                <p className="text-sm text-slate-400">Waiting for replies...</p>
              )}

              {round.results && (
                <div className="grid gap-3 md:grid-cols-2">
                  {round.results.map((r) => (
                    <div key={r.id} className="rounded-lg border border-slate-800 p-3">
                      <p className="text-xs font-semibold text-slate-300">{r.displayName || r.hostname}</p>
                      <p className={`mt-1 text-sm whitespace-pre-wrap ${r.error ? 'text-rose-400' : 'text-slate-300'}`}>
                        {r.error ? `Couldn't reach agent: ${r.error}` : (r.reply || '(no response)')}
                      </p>
                    </div>
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>

        <div className="mt-5 flex flex-wrap gap-2">
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
            placeholder="Ask the fleet something..."
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
      </Card>
    </div>
  );
}
