// Central fleet chat: answers operator questions using fleet context and
// optionally routes specific requests to individual agents.

import OpenAI from 'openai';
import { config } from '../config/index.js';
import { getFleet } from './fleetCache.js';
import { sendAgentMessage } from './fleet.js';
import { pendingApprovals, concerns } from './activity.js';
import { logger } from '../utils/logger.js';

// ---------------------------------------------------------------------------
// Fleet context serialisation (compact, Q&A-oriented)
// ---------------------------------------------------------------------------

function buildFleetContext(agents) {
  if (!agents || agents.length === 0) return 'No agents are enrolled yet.';

  const offline = agents.filter((a) => a.status === 'offline');
  const attention = agents.filter((a) => a.status === 'attention');
  const allApprovals = pendingApprovals(agents);
  const allConcerns = concerns(agents);

  const lines = [
    '## Fleet State',
    `- ${agents.length} agent(s) enrolled`,
    `- ${agents.length - offline.length - attention.length} healthy/idle`,
    attention.length > 0 ? `- ${attention.length} need attention` : null,
    offline.length > 0 ? `- ${offline.length} offline` : null,
    allApprovals.length > 0
      ? `- ${allApprovals.length} pending approval(s) (${allApprovals.filter((a) => a.risk === 'high').length} high-risk)`
      : '- No pending approvals',
    allConcerns.length > 0 ? `- ${allConcerns.length} open concern(s)` : null,
  ].filter(Boolean);

  lines.push('', '## Agents');
  for (const agent of agents) {
    lines.push('');
    // Include id prominently for routing.
    lines.push(`### ${agent.hostname} [id: ${agent.id}]`);
    lines.push(`- Role: ${agent.role || 'endpoint agent'} | OS: ${agent.os || agent.osType || 'unknown'} | Status: ${agent.statusLabel}`);
    if (agent.lastPatchedAt) {
      const daysAgo = Math.round((Date.now() - new Date(agent.lastPatchedAt).getTime()) / 86400000);
      lines.push(`- Last patched: ${daysAgo}d ago`);
    } else {
      lines.push(`- Last patched: never`);
    }

    const entries = (agent.timeline || [])
      .filter((e) => e.type !== 'approval')
      .slice(0, 5);
    if (entries.length > 0) {
      lines.push('- Recent activity:');
      for (const e of entries) {
        const ageH = e.time ? Math.round((Date.now() - new Date(e.time).getTime()) / 3600000) : null;
        lines.push(`  • [${e.type}${e.severity ? '/' + e.severity : ''}] ${e.title}${ageH != null ? ` (${ageH}h ago)` : ''}`);
      }
    }
  }

  if (allApprovals.length > 0) {
    lines.push('', '## Pending Approvals');
    for (const a of allApprovals) {
      lines.push(`- [${(a.risk || '?').toUpperCase()} RISK] ${a.hostname}: ${a.title}`);
    }
  }

  if (allConcerns.length > 0) {
    lines.push('', '## Open Concerns');
    for (const c of allConcerns) {
      lines.push(`- [${c.severity?.toUpperCase()}] ${c.hostname}: ${c.title}`);
    }
  }

  return lines.join('\n');
}

// ---------------------------------------------------------------------------
// LLM system prompt
// ---------------------------------------------------------------------------

const BASE_SYSTEM_PROMPT = `You are a fleet management assistant for "agent_patches" — a system where AI agents autonomously monitor and manage servers. You have access to the current state of all enrolled agents.

Your job is to answer the operator's questions conversationally and helpfully. You can:
1. Answer questions about the fleet directly from the provided fleet state data.
2. Route requests to individual agents when live interaction or an action on a specific host is needed.

Respond ONLY with a JSON object — no preamble, no markdown fences:
{
  "reply": "Your conversational response to the operator.",
  "forward": null | {
    "agentId": "short-hostname",
    "message": "Clear instruction or question for the agent (as if the operator typed it directly)"
  }
}

Rules:
- "reply" is always required. If forwarding, keep "reply" brief ("Checking with <hostname>...").
- Use "forward" when: the user asks to run a diagnostic, take an action, or get live data from a specific named agent.
- Do NOT forward when: the question can be answered from the fleet state above.
- "agentId" must exactly match an agent's id (shown in square brackets in the fleet data).
- Never forward to an agent that is offline.
- If asked about an offline agent, say so in "reply" and set forward to null.
- Be concise. The operator is a sysadmin.`;

// ---------------------------------------------------------------------------
// Chat
// ---------------------------------------------------------------------------

export async function chat(message, history = []) {
  if (!config.intelligence.baseUrl) {
    return {
      reply: 'Fleet Intelligence is not configured. Set INTELLIGENCE_BASE_URL in your central-backend environment to enable fleet chat.',
      routedTo: null,
    };
  }

  const client = new OpenAI({
    apiKey: config.intelligence.apiKey,
    baseURL: config.intelligence.baseUrl,
  });

  const agents = getFleet();
  const fleetContext = buildFleetContext(agents);
  const systemContent = `${BASE_SYSTEM_PROMPT}\n\n## Current Fleet State\n\n${fleetContext}`;

  // Build LLM messages from conversation history.
  const llmMessages = [{ role: 'system', content: systemContent }];
  for (const h of history) {
    if (h.role === 'user' || h.role === 'assistant') {
      llmMessages.push({ role: h.role, content: h.text });
    }
  }
  llmMessages.push({ role: 'user', content: message });

  let raw;
  try {
    const response = await client.chat.completions.create({
      model: config.intelligence.model,
      max_tokens: 800,
      messages: llmMessages,
    });
    raw = response.choices[0]?.message?.content ?? '';
  } catch (err) {
    logger.error(`centralChat: LLM call failed: ${err.message}`);
    return { reply: `Fleet intelligence is temporarily unavailable: ${err.message}`, routedTo: null };
  }

  let parsed;
  try {
    const cleaned = raw.replace(/^```(?:json)?\n?/, '').replace(/\n?```$/, '').trim();
    parsed = JSON.parse(cleaned);
  } catch {
    // LLM returned plain text — use it as-is.
    return { reply: raw.trim(), routedTo: null };
  }

  const reply = parsed.reply ?? '';
  const forward = parsed.forward;

  if (!forward || !forward.agentId || !forward.message) {
    return { reply, routedTo: null };
  }

  // Validate the agent exists and is not offline.
  const targetAgent = (agents ?? []).find((a) => a.id === forward.agentId);
  if (!targetAgent) {
    return { reply: `${reply}\n\n(Could not route: agent "${forward.agentId}" not found.)`, routedTo: null };
  }
  if (targetAgent.status === 'offline') {
    return { reply: `${reply}\n\n(${targetAgent.hostname} is offline — cannot reach it right now.)`, routedTo: null };
  }

  logger.info(`centralChat: routing to agent ${forward.agentId}: "${forward.message}"`);

  let agentReply;
  try {
    agentReply = await sendAgentMessage(forward.agentId, forward.message);
  } catch (err) {
    logger.warn(`centralChat: agent ${forward.agentId} returned error: ${err.message}`);
    return {
      reply,
      routedTo: { id: targetAgent.id, hostname: targetAgent.hostname },
      agentReply: `Could not reach agent: ${err.message}`,
    };
  }

  return {
    reply,
    routedTo: { id: targetAgent.id, hostname: targetAgent.hostname },
    agentReply,
  };
}
