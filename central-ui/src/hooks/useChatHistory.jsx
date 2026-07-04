import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import {
  fetchChatHistory,
  clearChatHistory,
  sendCentralChat,
  sendAgentMessage,
  broadcastMessage,
} from '../api/client';

// Well-known chat ids shared with the backend. Individual agent chats use
// the agent id itself.
export const FLEET_CHAT_ID = '__fleet__';
export const BROADCAST_CHAT_ID = '__broadcast__';

const ChatHistoryContext = createContext(null);

// Build the history array POST /chat/central expects from our message list.
// Combines assistant + adjacent agent replies into a single assistant turn.
function buildCentralHistory(messages) {
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

// Holds every chat thread (dashboard fleet chat, broadcast chat, and
// per-agent chats) so conversations survive page changes within a session.
// The backend keeps the durable per-user copy, which we hydrate from on
// login/reload. Send operations live here — not in the chat components — so
// an in-flight reply still lands in the history after the page that started
// it unmounts.
export function ChatHistoryProvider({ children }) {
  const [histories, setHistories] = useState({});
  const [sendingMap, setSendingMap] = useState({});
  const [hydrated, setHydrated] = useState(false);
  // Mirror of `histories` readable synchronously from async send flows.
  const historiesRef = useRef(histories);

  const applyHistories = useCallback((updater) => {
    setHistories((prev) => {
      const next = typeof updater === 'function' ? updater(prev) : updater;
      historiesRef.current = next;
      return next;
    });
  }, []);

  const addMessage = useCallback((chatId, message) => {
    applyHistories((prev) => ({ ...prev, [chatId]: [...(prev[chatId] ?? []), message] }));
  }, [applyHistories]);

  const setSending = useCallback((chatId, sending) => {
    setSendingMap((prev) => ({ ...prev, [chatId]: sending }));
  }, []);

  // Hydrate from the backend once per login. If the server reports replies
  // still in flight (e.g. the page was reloaded mid-send), show those chats
  // as sending and poll until each reply lands, then take the server copy.
  useEffect(() => {
    let cancelled = false;
    let timer = null;

    const watchPending = (pendingIds) => {
      let waiting = pendingIds;
      timer = setInterval(async () => {
        try {
          const { chats, pending } = await fetchChatHistory();
          if (cancelled) return;
          const still = new Set(pending);
          waiting.filter((id) => !still.has(id)).forEach((id) => {
            setSending(id, false);
            applyHistories((prev) => ({ ...prev, [id]: chats[id] ?? [] }));
          });
          waiting = waiting.filter((id) => still.has(id));
          if (waiting.length === 0) {
            clearInterval(timer);
            timer = null;
          }
        } catch {
          // transient — keep polling
        }
      }, 3000);
    };

    (async () => {
      try {
        const { chats, pending } = await fetchChatHistory();
        if (cancelled) return;
        applyHistories(chats ?? {});
        if (pending?.length > 0) {
          pending.forEach((id) => setSending(id, true));
          watchPending(pending);
        }
      } catch {
        // history unavailable — fall back to in-memory only
      } finally {
        if (!cancelled) setHydrated(true);
      }
    })();

    return () => {
      cancelled = true;
      if (timer) clearInterval(timer);
    };
  }, [applyHistories, setSending]);

  // Dashboard fleet AI chat. Returns the agent the backend routed to, if any.
  const sendCentralMessage = useCallback(async (text) => {
    const history = buildCentralHistory(historiesRef.current[FLEET_CHAT_ID] ?? []);
    addMessage(FLEET_CHAT_ID, { role: 'user', text });
    setSending(FLEET_CHAT_ID, true);
    try {
      const { reply, routedTo, agentReply } = await sendCentralChat(text, history);
      addMessage(FLEET_CHAT_ID, { role: 'assistant', text: reply, routedTo });
      if (agentReply) {
        addMessage(FLEET_CHAT_ID, { role: 'agent', text: agentReply, agentId: routedTo.id, hostname: routedTo.hostname });
      }
      return routedTo ?? null;
    } catch (err) {
      addMessage(FLEET_CHAT_ID, { role: 'assistant', text: `Error: ${err.message}` });
      return null;
    } finally {
      setSending(FLEET_CHAT_ID, false);
    }
  }, [addMessage, setSending]);

  // Direct chat with a single agent (AgentDetail Interact tab).
  const sendToAgent = useCallback(async (agentId, text) => {
    addMessage(agentId, { role: 'user', text });
    setSending(agentId, true);
    try {
      const { reply } = await sendAgentMessage(agentId, text);
      addMessage(agentId, { role: 'agent', text: reply || '(no response)' });
    } catch (err) {
      addMessage(agentId, { role: 'agent', text: `Couldn't reach the agent: ${err.message}` });
    } finally {
      setSending(agentId, false);
    }
  }, [addMessage, setSending]);

  // Fleet-wide broadcast (Fleet Chat page).
  const sendBroadcast = useCallback(async (text) => {
    addMessage(BROADCAST_CHAT_ID, { role: 'user', text });
    setSending(BROADCAST_CHAT_ID, true);
    try {
      const { results } = await broadcastMessage(text);
      addMessage(BROADCAST_CHAT_ID, { role: 'results', results });
    } catch (err) {
      addMessage(BROADCAST_CHAT_ID, { role: 'results', results: [], error: err.message });
    } finally {
      setSending(BROADCAST_CHAT_ID, false);
    }
  }, [addMessage, setSending]);

  const clearChat = useCallback((chatId) => {
    applyHistories((prev) => {
      const next = { ...prev };
      delete next[chatId];
      return next;
    });
    // Local clear applies even if the backend call fails.
    clearChatHistory(chatId).catch(() => {});
  }, [applyHistories]);

  const value = useMemo(
    () => ({
      histories,
      sendingMap,
      hydrated,
      addMessage,
      setSending,
      clearChat,
      sendCentralMessage,
      sendToAgent,
      sendBroadcast,
    }),
    [histories, sendingMap, hydrated, addMessage, setSending, clearChat, sendCentralMessage, sendToAgent, sendBroadcast]
  );

  return <ChatHistoryContext.Provider value={value}>{children}</ChatHistoryContext.Provider>;
}

export function useChatHistory(chatId) {
  const ctx = useContext(ChatHistoryContext);
  const messages = ctx.histories[chatId] ?? [];
  const sending = !!ctx.sendingMap[chatId];
  const add = useCallback((msg) => ctx.addMessage(chatId, msg), [ctx.addMessage, chatId]); // eslint-disable-line react-hooks/exhaustive-deps
  const clear = useCallback(() => ctx.clearChat(chatId), [ctx.clearChat, chatId]); // eslint-disable-line react-hooks/exhaustive-deps
  return {
    messages,
    sending,
    hydrated: ctx.hydrated,
    addMessage: add,
    clearChat: clear,
    sendCentralMessage: ctx.sendCentralMessage,
    sendToAgent: ctx.sendToAgent,
    sendBroadcast: ctx.sendBroadcast,
  };
}
