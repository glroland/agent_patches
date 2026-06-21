import { createContext, useCallback, useContext, useState } from 'react';

const ChatHistoryContext = createContext(null);

export function ChatHistoryProvider({ children }) {
  const [histories, setHistories] = useState({});

  const addMessage = useCallback((agentId, message) => {
    setHistories((prev) => ({
      ...prev,
      [agentId]: [...(prev[agentId] ?? []), message],
    }));
  }, []);

  return (
    <ChatHistoryContext.Provider value={{ histories, addMessage }}>
      {children}
    </ChatHistoryContext.Provider>
  );
}

export function useChatHistory(agentId) {
  const { histories, addMessage } = useContext(ChatHistoryContext);
  const messages = histories[agentId] ?? [];
  const add = useCallback((msg) => addMessage(agentId, msg), [addMessage, agentId]);
  return { messages, addMessage: add };
}
