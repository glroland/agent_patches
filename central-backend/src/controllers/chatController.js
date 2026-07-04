import * as fleet from '../services/fleet.js';
import * as centralChat from '../services/centralChat.js';
import * as chatStore from '../services/chatStore.js';
import { CENTRAL_CHAT_ID, BROADCAST_CHAT_ID } from '../services/chatStore.js';

// POST /api/chat — broadcast an operator chat message to every agent in the
// fleet and return each agent's reply (or error). Both sides are recorded in
// the user's persistent history, so the exchange survives even if the
// browser navigates away before the replies arrive.
export async function broadcastMessage(req, res, next) {
  const username = req.user.username;
  try {
    const { message } = req.body ?? {};
    if (typeof message !== 'string' || !message.trim()) {
      return res.status(400).json({ error: 'invalid_request', message: '"message" is required' });
    }

    chatStore.appendMessage(username, BROADCAST_CHAT_ID, { role: 'user', text: message });
    chatStore.setPending(username, BROADCAST_CHAT_ID, true);
    try {
      const results = await fleet.broadcastMessage(message);
      chatStore.appendMessage(username, BROADCAST_CHAT_ID, { role: 'results', results });
      res.json({ results });
    } catch (err) {
      chatStore.appendMessage(username, BROADCAST_CHAT_ID, { role: 'results', results: [], error: err.message });
      throw err;
    } finally {
      chatStore.setPending(username, BROADCAST_CHAT_ID, false);
    }
  } catch (err) {
    next(err);
  }
}

// POST /api/chat/central — fleet-level AI chat. Answers from fleet context or
// routes to a specific agent. The exchange is recorded in the user's
// persistent history.
export async function centralChatMessage(req, res, next) {
  const username = req.user.username;
  try {
    const { message, history } = req.body ?? {};
    if (typeof message !== 'string' || !message.trim()) {
      return res.status(400).json({ error: 'invalid_request', message: '"message" is required' });
    }
    if (history !== undefined && !Array.isArray(history)) {
      return res.status(400).json({ error: 'invalid_request', message: '"history" must be an array' });
    }

    chatStore.appendMessage(username, CENTRAL_CHAT_ID, { role: 'user', text: message });
    chatStore.setPending(username, CENTRAL_CHAT_ID, true);
    try {
      const result = await centralChat.chat(message, history ?? []);
      chatStore.appendMessage(username, CENTRAL_CHAT_ID, {
        role: 'assistant',
        text: result.reply,
        routedTo: result.routedTo ?? undefined,
      });
      if (result.agentReply) {
        chatStore.appendMessage(username, CENTRAL_CHAT_ID, {
          role: 'agent',
          text: result.agentReply,
          agentId: result.routedTo?.id,
          hostname: result.routedTo?.hostname,
        });
      }
      res.json(result);
    } catch (err) {
      chatStore.appendMessage(username, CENTRAL_CHAT_ID, { role: 'assistant', text: `Error: ${err.message}` });
      throw err;
    } finally {
      chatStore.setPending(username, CENTRAL_CHAT_ID, false);
    }
  } catch (err) {
    next(err);
  }
}

// GET /api/chat/history — every persisted chat thread for the current user,
// plus the ids of chats with a reply still in flight on this backend.
export function getChatHistory(req, res) {
  res.json(chatStore.getHistory(req.user.username));
}

// DELETE /api/chat/history/:chatId — clear one chat thread for the current user.
export function clearChatHistory(req, res) {
  chatStore.clearChat(req.user.username, req.params.chatId);
  res.json({ cleared: true });
}
