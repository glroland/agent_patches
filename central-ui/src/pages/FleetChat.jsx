import { useState } from 'react';
import Card from '../components/Card';
import { ChatIcon, TrashIcon } from '../components/icons';
import { useChatHistory, BROADCAST_CHAT_ID } from '../hooks/useChatHistory';

const SUGGESTIONS = [
  'What are you working on right now?',
  'Summarize what you have seen recently',
  'Re-check everything now',
];

export default function FleetChat() {
  const { messages, sending, clearChat, sendBroadcast } = useChatHistory(BROADCAST_CHAT_ID);
  const [input, setInput] = useState('');

  const send = (text) => {
    const value = (text ?? input).trim();
    if (!value || sending) return;
    setInput('');
    sendBroadcast(value);
  };

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold text-black">Fleet Chat</h1>
        <p className="mt-1 text-sm text-navy-500">
          Send a message to every agent in the fleet at once and compare their replies.
        </p>
      </div>

      <Card
        title="Broadcast"
        subtitle="Sends a message to all agents via the A2A JSON-RPC API"
        action={
          messages.length > 0 && (
            <button
              onClick={clearChat}
              disabled={sending}
              title="Clear chat history"
              className="inline-flex items-center gap-1.5 rounded-lg border border-navy-300 px-2.5 py-1.5 text-xs font-medium text-navy-600 hover:border-navy-400 hover:text-navy-800 disabled:opacity-50 transition-colors"
            >
              <TrashIcon className="h-3.5 w-3.5" /> Clear
            </button>
          )
        }
      >
        <div className="space-y-5">
          {messages.length === 0 && (
            <p className="text-sm text-navy-500">Ask all agents something to get started.</p>
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

            if (m.role === 'results') {
              return (
                <div key={i} className="space-y-3">
                  {m.error && (
                    <p className="text-sm text-rose-700">Broadcast failed: {m.error}</p>
                  )}
                  {m.results?.length > 0 && (
                    <div className="grid gap-3 md:grid-cols-2">
                      {m.results.map((r) => (
                        <div key={r.id} className="rounded-lg border border-navy-200 p-3">
                          <p className="text-xs font-semibold text-navy-700">{r.displayName || r.hostname}</p>
                          <p className={`mt-1 text-sm whitespace-pre-wrap ${r.error ? 'text-rose-700' : 'text-navy-700'}`}>
                            {r.error ? `Couldn't reach agent: ${r.error}` : (r.reply || '(no response)')}
                          </p>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              );
            }

            return null;
          })}

          {sending && (
            <p className="text-sm text-navy-600">Waiting for replies...</p>
          )}
        </div>

        <div className="mt-5 flex flex-wrap gap-2">
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
            placeholder="Ask the fleet something..."
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
      </Card>
    </div>
  );
}
