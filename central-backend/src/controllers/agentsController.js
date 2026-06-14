import { notImplemented } from '../utils/notImplemented.js';
import * as agentRegistry from '../services/agentRegistry.js';

// GET /api/agents
export function listAgents(req, res, next) {
  try {
    res.json(agentRegistry.listAgents());
  } catch (err) {
    next(err);
  }
}

// GET /api/agents/:id
export function getAgent(req, res, next) {
  try {
    const agent = agentRegistry.getAgent(req.params.id);
    if (!agent) {
      return res.status(404).json({ error: 'not_found', message: `No agent with id "${req.params.id}"` });
    }
    res.json(agent);
  } catch (err) {
    next(err);
  }
}

// GET /api/agents/:id/activity
export const getAgentActivity = notImplemented;

// POST /api/agents/:id/messages — relay an operator message/task to the agent
export const sendAgentMessage = notImplemented;
