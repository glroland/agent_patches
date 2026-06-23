import * as fleet from '../services/fleet.js';
import * as centralChat from '../services/centralChat.js';

// POST /api/chat — broadcast an operator chat message to every agent in the
// fleet and return each agent's reply (or error).
export async function broadcastMessage(req, res, next) {
  try {
    const { message } = req.body ?? {};
    if (typeof message !== 'string' || !message.trim()) {
      return res.status(400).json({ error: 'invalid_request', message: '"message" is required' });
    }

    const results = await fleet.broadcastMessage(message);
    res.json({ results });
  } catch (err) {
    next(err);
  }
}

// POST /api/chat/central — fleet-level AI chat. Answers from fleet context or
// routes to a specific agent.
export async function centralChatMessage(req, res, next) {
  try {
    const { message, history } = req.body ?? {};
    if (typeof message !== 'string' || !message.trim()) {
      return res.status(400).json({ error: 'invalid_request', message: '"message" is required' });
    }
    if (history !== undefined && !Array.isArray(history)) {
      return res.status(400).json({ error: 'invalid_request', message: '"history" must be an array' });
    }

    const result = await centralChat.chat(message, history ?? []);
    res.json(result);
  } catch (err) {
    next(err);
  }
}
