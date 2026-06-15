import * as fleet from '../services/fleet.js';

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
