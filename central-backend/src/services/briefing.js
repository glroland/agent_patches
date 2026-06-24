// Operator briefing: a short, urgent-focused summary generated via LLM
// whenever the fleet state updates and the cache has expired (5 min TTL).
// Invalidated immediately when approvals are actioned.

import OpenAI from 'openai';
import { config } from '../config/index.js';
import { logger } from '../utils/logger.js';
import { getFleet, subscribe as subscribeFleet } from './fleetCache.js';
import { pendingApprovals, concerns, recentActivity } from './activity.js';
import { setBriefing, isStale } from './briefingCache.js';

// ---------------------------------------------------------------------------
// Fleet serialisation — compact, urgency-focused
// ---------------------------------------------------------------------------

function serializeForBriefing(agents, nowMs) {
  if (!agents || agents.length === 0) return 'No agents are enrolled yet.';

  const offline = agents.filter((a) => a.status === 'offline');
  const attention = agents.filter((a) => a.status === 'attention');
  const allApprovals = pendingApprovals(agents);
  const allConcerns = concerns(agents);
  const criticalConcerns = allConcerns.filter((c) => c.severity === 'critical');

  const utcHour = new Date(nowMs).getUTCHours();
  const lines = [`## Context\n- Current UTC hour: ${utcHour}`];

  lines.push(
    '',
    '## Fleet Overview',
    `- ${agents.length} agent(s) enrolled`,
    `- ${agents.length - offline.length - attention.length} healthy/idle`,
    attention.length > 0 ? `- ${attention.length} need attention` : null,
    offline.length > 0 ? `- ${offline.length} offline` : null,
    allApprovals.length > 0
      ? `- ${allApprovals.length} pending approval(s) (${allApprovals.filter((a) => a.risk === 'high').length} high-risk)`
      : '- No pending approvals',
    criticalConcerns.length > 0 ? `- ${criticalConcerns.length} critical concern(s)` : null,
  ).filter(Boolean);

  // Pending approvals with age
  if (allApprovals.length > 0) {
    lines.push('', '## Pending Approvals');
    for (const a of allApprovals) {
      const ageH = a.time ? Math.round((nowMs - new Date(a.time).getTime()) / 3600000) : null;
      lines.push(
        `- [${(a.risk || '?').toUpperCase()} RISK] ${a.hostname}: ${a.title}${ageH != null ? ` (waiting ${ageH}h)` : ''}`,
      );
    }
  }

  // Critical / warning concerns
  if (allConcerns.length > 0) {
    lines.push('', '## Open Concerns');
    for (const c of allConcerns) {
      lines.push(`- [${(c.severity || 'unknown').toUpperCase()}] ${c.hostname}: ${c.title}`);
      if (c.detail) lines.push(`  ${c.detail.slice(0, 150)}`);
    }
  }

  // Agents needing attention with their latest critical/warning timeline entries
  if (attention.length > 0 || offline.length > 0) {
    lines.push('', '## Agents Needing Attention');
    for (const a of [...attention, ...offline]) {
      lines.push(`- ${a.hostname} [${a.statusLabel}]`);
      const urgent = (a.timeline || [])
        .filter((e) => e.severity === 'critical' || e.severity === 'warning')
        .slice(0, 2);
      for (const e of urgent) {
        lines.push(`  • [${e.severity}] ${e.title}`);
      }
    }
  }

  // Most recent critical activity across fleet
  const critical = recentActivity(agents, 50)
    .filter((e) => e.severity === 'critical')
    .slice(0, 5);
  if (critical.length > 0) {
    lines.push('', '## Recent Critical Events');
    for (const e of critical) {
      const ageH = e.time ? Math.round((nowMs - new Date(e.time).getTime()) / 3600000) : null;
      lines.push(`- [${e.hostname || '?'}] ${e.title}${ageH != null ? ` (${ageH}h ago)` : ''}`);
    }
  }

  return lines.filter((l) => l != null).join('\n');
}

// ---------------------------------------------------------------------------
// LLM prompt
// ---------------------------------------------------------------------------

const SYSTEM_PROMPT = `You are the operations briefing assistant for "agent_patches" — a fleet of AI agents that autonomously monitor and manage servers.

Generate a short, actionable briefing for the operator opening the dashboard. Focus on what requires IMMEDIATE attention: critical health alerts, high-risk approvals waiting too long, imminent disk failures, security concerns, agents offline.

If the fleet is calm with nothing urgent, say so warmly — do not invent problems.

Respond ONLY with a JSON object — no preamble, no markdown fences:
{
  "greeting": "One sentence. Adapt to time of day (Good morning/afternoon/evening based on UTC hour provided). State overall fleet health concisely.",
  "urgency": "calm|watch|action",
  "items": [
    {
      "severity": "critical|warning|info",
      "title": "Short imperative title (50 chars max)",
      "detail": "1-2 sentences. Name the specific host, resource, or action needed."
    }
  ]
}

Rules:
- "urgency": "action" = something needs doing now; "watch" = worth monitoring soon; "calm" = all clear.
- Maximum 5 items, ordered critical → warning → info.
- Only include items with real evidence in the data. No invented concerns.
- Keep greeting under 25 words.
- If calm: items may be empty [].`;

// ---------------------------------------------------------------------------
// Generation
// ---------------------------------------------------------------------------

let _generating = false;

async function generate() {
  if (!config.intelligence.baseUrl) return;
  if (!isStale()) return;
  if (_generating) return;

  const agents = getFleet();
  if (!agents) return;

  _generating = true;
  const nowMs = Date.now();
  const model = config.intelligence.model;
  const baseURL = config.intelligence.baseUrl;

  try {
    logger.info(`briefing: generating (model: ${model})`);

    const client = new OpenAI({ apiKey: config.intelligence.apiKey, baseURL });
    const context = serializeForBriefing(agents, nowMs);

    const response = await client.chat.completions.create({
      model,
      max_tokens: 600,
      messages: [
        { role: 'system', content: SYSTEM_PROMPT },
        { role: 'user', content: context },
      ],
    });

    if (response?.error) {
      logger.error('briefing: LLM error body', { error: response.error });
      return;
    }

    const raw = response?.choices?.[0]?.message?.content?.trim() ?? '';
    if (!raw) {
      logger.error('briefing: empty LLM response');
      return;
    }

    let parsed;
    try {
      const cleaned = raw.replace(/^```(?:json)?\n?/, '').replace(/\n?```$/, '').trim();
      parsed = JSON.parse(cleaned);
    } catch {
      logger.error(`briefing: failed to parse response: ${raw.slice(0, 200)}`);
      return;
    }

    setBriefing({
      greeting: parsed.greeting ?? '',
      urgency: parsed.urgency ?? 'calm',
      items: Array.isArray(parsed.items) ? parsed.items : [],
      generatedAt: new Date(nowMs).toISOString(),
    });

    logger.info(`briefing: ready (urgency: ${parsed.urgency}, ${parsed.items?.length ?? 0} item(s))`);
  } catch (err) {
    logger.error(`briefing: LLM call failed: ${err.message}`);
  } finally {
    _generating = false;
  }
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

export function start() {
  if (!config.intelligence.baseUrl) {
    logger.info('briefing: INTELLIGENCE_BASE_URL not set — briefing disabled');
    return;
  }

  // Regenerate whenever fleet state changes, subject to TTL inside generate().
  subscribeFleet(() => generate());

  logger.info('briefing: service started');
}
