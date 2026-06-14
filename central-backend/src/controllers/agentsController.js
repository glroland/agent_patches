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

// POST /api/agents/:id/messages — relay an operator message/task to the agent
export const sendAgentMessage = notImplemented;
