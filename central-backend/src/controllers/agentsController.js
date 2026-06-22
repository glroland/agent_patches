import { notImplemented } from '../utils/notImplemented.js';
import * as fleet from '../services/fleet.js';
import { pendingApprovals } from '../services/activity.js';

// GET /api/agents — the Agents screen's fleet list: one row per agent with
// enough detail to render its card without a follow-up request.
export async function listAgents(req, res, next) {
  try {
    const agents = await fleet.listFleet();
    res.json(
      agents.map((agent) => {
        const latest = agent.timeline[0];
        return {
          id: agent.id,
          hostname: agent.hostname,
          displayName: agent.displayName,
          role: agent.role,
          os: agent.os,
          tags: agent.tags,
          status: agent.status,
          statusLabel: agent.statusLabel,
          statusDescription: agent.statusDescription,
          currentTask: agent.currentTask,
          lastPoll: agent.lastPoll,
          latestActivity: latest ? { title: latest.title, time: latest.time, type: latest.type } : null,
          pendingApprovalCount: pendingApprovals([agent]).length,
        };
      })
    );
  } catch (err) {
    next(err);
  }
}

// GET /api/agents/:id — the AgentDetail screen: full agent profile plus its
// complete activity timeline.
export async function getAgent(req, res, next) {
  try {
    const agent = await fleet.getFleetAgent(req.params.id);
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

// GET /api/agents/:id/memory — the Agent Memory tab: current snapshot of
// every memory domain plus all attrs, as reported by the agent's GET /memory
// endpoint.
export async function getAgentMemory(req, res, next) {
  try {
    const memory = await fleet.getAgentMemory(req.params.id);
    if (memory === undefined) {
      return res.status(404).json({ error: 'not_found', message: `No agent with id "${req.params.id}"` });
    }
    if (memory === null) {
      return res.status(502).json({ error: 'agent_unreachable', message: 'agent did not respond to GET /memory' });
    }
    res.json(memory);
  } catch (err) {
    next(err);
  }
}

// DELETE /api/agents/:id/memory — clear all memory on a single agent.
export async function clearAgentMemory(req, res, next) {
  try {
    const result = await fleet.clearAgentMemory(req.params.id);
    if (result === undefined) {
      return res.status(404).json({ error: 'not_found', message: `No agent with id "${req.params.id}"` });
    }
    if (result === null) {
      return res.status(502).json({ error: 'agent_unreachable', message: 'agent did not respond to DELETE /memory' });
    }
    // Re-poll in the background to flush stale timeline data from the fleet cache.
    fleet.refreshFleet().catch(() => {});
    res.json({ cleared: true });
  } catch (err) {
    next(err);
  }
}

// GET /api/agents/:id/responsibilities — scheduled responsibilities with live
// scheduling state and last-run outcome from the agent.
export async function getAgentResponsibilities(req, res, next) {
  try {
    const responsibilities = await fleet.getAgentResponsibilities(req.params.id);
    if (responsibilities === undefined) {
      return res.status(404).json({ error: 'not_found', message: `No agent with id "${req.params.id}"` });
    }
    if (responsibilities === null) {
      return res.status(502).json({ error: 'agent_unreachable', message: 'agent did not respond to GET /responsibilities' });
    }
    res.json(responsibilities);
  } catch (err) {
    next(err);
  }
}

// POST /api/agents/:id/messages — relay an operator chat message to the
// agent and return its reply.
export async function sendAgentMessage(req, res, next) {
  try {
    const { message } = req.body ?? {};
    if (typeof message !== 'string' || !message.trim()) {
      return res.status(400).json({ error: 'invalid_request', message: '"message" is required' });
    }

    const reply = await fleet.sendAgentMessage(req.params.id, message);
    if (reply === undefined) {
      return res.status(404).json({ error: 'not_found', message: `No agent with id "${req.params.id}"` });
    }

    res.json({ reply });
  } catch (err) {
    res.status(502).json({ error: 'agent_unreachable', message: err.message });
  }
}
