// Fleet intelligence: periodically analyses the full fleet state using an
// OpenAI-compatible API and produces structured recommendations. Results are
// stored in intelligenceCache and pushed to connected UI clients via the WS hub.

import OpenAI from 'openai';
import { config } from '../config/index.js';
import { logger } from '../utils/logger.js';
import { getFleet } from './fleetCache.js';
import { pendingApprovals, concerns } from './activity.js';
import { setReport } from './intelligenceCache.js';
import { getStats } from './gatewayService.js';
import * as fleet from './fleet.js';

let _client = null;
let _timer = null;

// ---------------------------------------------------------------------------
// Fleet serialisation — compact, LLM-readable summary of fleet state.
// ---------------------------------------------------------------------------

function serializeFleet(agents) {
  if (!agents || agents.length === 0) return 'No agents are enrolled yet.';

  const allApprovals = pendingApprovals(agents);
  const allConcerns = concerns(agents);
  const offline = agents.filter((a) => a.status === 'offline');
  const attention = agents.filter((a) => a.status === 'attention');

  const lines = [
    '## Fleet Overview',
    `- ${agents.length} agent(s) enrolled`,
    `- ${agents.length - offline.length - attention.length} healthy / idle`,
    attention.length > 0 ? `- ${attention.length} need attention` : null,
    offline.length > 0 ? `- ${offline.length} offline` : null,
    allApprovals.length > 0
      ? `- ${allApprovals.length} pending approval(s) (${allApprovals.filter((a) => a.risk === 'high').length} high-risk)`
      : '- No pending approvals',
    allConcerns.length > 0 ? `- ${allConcerns.length} open concern(s)` : null,
  ].filter(Boolean);

  lines.push('', '## Agents');

  for (const agent of agents) {
    lines.push('', `### ${agent.hostname} (${agent.role || 'endpoint agent'})`);
    lines.push(`- OS: ${agent.os || agent.osType || 'unknown'}`);
    lines.push(`- Status: ${agent.statusLabel}`);
    if (agent.currentTask) lines.push(`- Currently: ${agent.currentTask}`);
    if (agent.lastPoll) {
      const ageMin = Math.round((Date.now() - new Date(agent.lastPoll).getTime()) / 60000);
      lines.push(`- Last seen: ${ageMin}m ago`);
    }

    // Up to 8 recent non-approval timeline entries with full detail.
    const entries = (agent.timeline || [])
      .filter((e) => e.type !== 'approval')
      .slice(0, 8);

    if (entries.length > 0) {
      lines.push('- Recent activity:');
      for (const e of entries) {
        const ageH = e.time
          ? Math.round((Date.now() - new Date(e.time).getTime()) / 3600000)
          : null;
        lines.push(`  • [${e.type}] ${e.title}${ageH != null ? ` (${ageH}h ago)` : ''}`);
        if (e.detail) {
          const detail = e.detail.length > 300 ? e.detail.slice(0, 297) + '…' : e.detail;
          lines.push(`    ${detail}`);
        }
      }
    }
  }

  if (allApprovals.length > 0) {
    lines.push('', '## Pending Approvals');
    for (const a of allApprovals) {
      const ageH = a.time
        ? Math.round((Date.now() - new Date(a.time).getTime()) / 3600000)
        : null;
      lines.push(`- [${(a.risk || 'unknown').toUpperCase()} RISK] ${a.hostname}: ${a.title}${ageH != null ? ` (waiting ${ageH}h)` : ''}`);
      if (a.detail) {
        const detail = a.detail.length > 200 ? a.detail.slice(0, 197) + '…' : a.detail;
        lines.push(`  ${detail}`);
      }
    }
  }

  if (allConcerns.length > 0) {
    lines.push('', '## Open Concerns');
    for (const c of allConcerns) {
      lines.push(`- [${c.severity?.toUpperCase()}] ${c.hostname}: ${c.title}`);
      if (c.detail) lines.push(`  ${c.detail}`);
    }
  }

  return lines.join('\n');
}

// ---------------------------------------------------------------------------
// Approval history serialisation
// ---------------------------------------------------------------------------

function serializeApprovalHistory(agentDetails) {
  const hasAny = agentDetails.some(({ memory }) => {
    if (!memory?.attrs) return false;
    return Object.keys(memory.attrs).some((k) => k.startsWith('approval:'));
  });
  if (!hasAny) return null;

  const lines = ['## Approval History (full lifetime of agent memory)'];

  for (const { hostname, memory } of agentDetails) {
    if (!memory?.attrs) continue;

    const approvals = Object.entries(memory.attrs)
      .filter(([key]) => key.startsWith('approval:'))
      .map(([, val]) => val)
      .filter(Boolean);

    if (approvals.length === 0) continue;

    approvals.sort((a, b) => new Date(a.requested_at) - new Date(b.requested_at));

    const byStatus = (s) => approvals.filter((a) => a.status === s);
    const approved  = byStatus('approved');
    const rejected  = byStatus('rejected');
    const timedOut  = byStatus('timed_out');
    const cancelled = byStatus('cancelled');
    const pending   = byStatus('pending');

    lines.push('', `### ${hostname}`);
    lines.push(
      `- Totals: ${approvals.length} total — ` +
      `${approved.length} approved, ${rejected.length} rejected, ` +
      `${timedOut.length} timed out, ${cancelled.length} cancelled, ${pending.length} pending`
    );

    for (const a of approvals) {
      const requestedAgo = a.requested_at
        ? Math.round((Date.now() - new Date(a.requested_at).getTime()) / 3600000) + 'h ago'
        : null;
      const decidedAgo = a.decided_at
        ? Math.round((Date.now() - new Date(a.decided_at).getTime()) / 3600000) + 'h ago'
        : null;

      lines.push(`- [${(a.status ?? 'unknown').toUpperCase()}] [${(a.risk ?? '?').toUpperCase()} RISK] ${a.title}`);
      if (a.proposed_action) {
        const pa = a.proposed_action.length > 200 ? a.proposed_action.slice(0, 197) + '…' : a.proposed_action;
        lines.push(`  - Proposed action: ${pa}`);
      }
      if (requestedAgo) lines.push(`  - Requested: ${requestedAgo}`);
      if (decidedAgo)   lines.push(`  - Decided: ${decidedAgo}`);
      if (a.reason)     lines.push(`  - Operator reason: ${a.reason}`);
      if (a.retry_count > 0) lines.push(`  - Retried ${a.retry_count}x`);
    }
  }

  return lines.join('\n');
}

// ---------------------------------------------------------------------------
// Token / resource stats serialisation
// ---------------------------------------------------------------------------

function serializeTokenStats(gatewayStats, agentDetails) {
  if (!gatewayStats && (!agentDetails || agentDetails.length === 0)) {
    return null;
  }

  const lines = ['## Token & Resource Statistics'];

  if (gatewayStats) {
    lines.push('', `### Gateway Overview (as of ${gatewayStats.generatedAt ?? 'unknown'})`,
      `- Upstream: ${gatewayStats.upstream ?? 'unknown'}`,
      `- Max concurrency: ${gatewayStats.maxConcurrency ?? '?'} / Queue capacity: ${gatewayStats.queueCapacity ?? '?'}`,
      `- Active requests: ${gatewayStats.activeRequests ?? 0} / Queued: ${gatewayStats.queuedRequests ?? 0}`,
    );

    if (gatewayStats.endpoints?.length > 0) {
      lines.push('', '### Per-Endpoint Token Usage');
      for (const ep of gatewayStats.endpoints) {
        const label = ep.name ? `${ep.name} (${ep.host})` : ep.host;
        lines.push(`- **${label}**`);
        lines.push(`  - Tokens: ${ep.tokensLastHour} last-hour / ${ep.tokensLastDay} last-day / ${ep.tokensTotal} total`);
        lines.push(`  - Requests: ${ep.requestsLastHour} last-hour / ${ep.requestsLastDay} last-day / ${ep.requestsTotal} total`);
        if (ep.pendingRequests > 0) lines.push(`  - Pending: ${ep.pendingRequests}`);
      }
    }

    if (gatewayStats.responsibilities?.length > 0) {
      lines.push('', '### Per-Responsibility Token Usage (across all endpoints)');
      for (const rs of gatewayStats.responsibilities) {
        lines.push(`- **${rs.name}**`);
        lines.push(`  - Tokens: ${rs.tokensLastHour} last-hour / ${rs.tokensLastDay} last-day / ${rs.tokensTotal} total`);
        lines.push(`  - Requests: ${rs.requestsLastHour} last-hour / ${rs.requestsLastDay} last-day / ${rs.requestsTotal} total`);
      }
    }
  }

  const withResponsibilities = agentDetails?.filter(({ responsibilities }) => responsibilities?.length > 0) ?? [];
  if (withResponsibilities.length > 0) {
    lines.push('', '### Per-Agent Responsibility Configuration & History');
    for (const { hostname, responsibilities } of withResponsibilities) {
      lines.push(``, `#### ${hostname}`);
      for (const r of responsibilities) {
        lines.push(`- **${r.name}** — ${r.schedule}`);
        lines.push(`  - Tools: ${r.tools?.join(', ') || 'none'}`);
        lines.push(`  - Instruction: ${r.instruction?.slice(0, 200) ?? '(none)'}${r.instruction?.length > 200 ? '…' : ''}`);
        lines.push(`  - Status: ${r.status}${r.lastRunAt ? ` (last run: ${r.lastRunAt})` : ''}`);
        if (r.nextRunAt) lines.push(`  - Next run: ${r.nextRunAt}`);
        if (r.summary) {
          const s = r.summary.length > 300 ? r.summary.slice(0, 297) + '…' : r.summary;
          lines.push(`  - Last summary: ${s}`);
        }
      }
    }
  }

  return lines.join('\n');
}

// ---------------------------------------------------------------------------
// Analysis
// ---------------------------------------------------------------------------

const SYSTEM_PROMPT = `You are the central intelligence layer for a fleet of AI server management agents called "agent_patches". Each endpoint agent autonomously monitors its host — checking for system patches, disk health, memory, network utilisation, interactive logins, and more — then requests operator approval before taking action.

Your job is to analyse the current fleet state, token consumption, responsibility scheduling data, and the complete approval/rejection history, then produce actionable recommendations. Think like a senior sysadmin and an AI engineer simultaneously:

1. Flag genuine health issues (offline agents, repeated failures, pending patches, resource concerns).
2. Spot patterns across the fleet (e.g. multiple hosts need patching, several high-risk approvals sitting idle).
3. Recommend new agent capabilities that would improve visibility or automation — be specific and concrete. For example: "Add a skill that monitors TLS certificate expiry" or "Agents should alert on failed systemd units."
4. Suggest configuration or architectural improvements when you see evidence for them.
5. Analyse token consumption by endpoint and by responsibility. Identify responsibilities that consume disproportionate tokens relative to the value they provide based on their output summaries and action history. Recommend frequency adjustments (increase if catching real issues, decrease if consistently finding nothing), tool list pruning (if a responsibility calls tools whose output it never uses), or instruction tightening. Be specific: name the responsibility, the host, the current schedule, and the proposed change.
6. Analyse the full approval and rejection history. Look for:
   - Actions that are consistently approved → the agent could be trusted to automate these or they could be reclassified to a lower risk tier.
   - Actions that are frequently rejected → the agent may be too aggressive; the triggering heuristic or threshold needs tuning.
   - High rates of timed-out approvals → operators are not responding in time; consider changing notification strategy, reducing the approval timeout, or automating low-risk variants.
   - Repeated retries on the same action type → the underlying condition is not being resolved; a new remediation skill may be needed.
   - Operator rejection reasons → extract signal from reason text to guide specific agent improvements.
   - Risk level mismatches → if low-risk requests are frequently rejected, or high-risk requests always approved without scrutiny, the risk classification logic needs adjustment.
   Produce a separate approvalInsights array with specific, evidence-based findings and recommendations for each pattern you find.

Respond ONLY with a JSON object in this exact shape — no preamble, no markdown fences:
{
  "headline": "One direct sentence summarising overall fleet health.",
  "recommendations": [
    {
      "priority": "high|medium|low",
      "category": "health|security|feature|configuration",
      "title": "Short imperative title (60 chars max)",
      "body": "2–4 sentences. Name the host, the pattern, or the exact feature to build."
    }
  ],
  "resourceOptimization": [
    {
      "priority": "high|medium|low",
      "hostname": "hostname or 'all'",
      "responsibility": "responsibility name",
      "currentSchedule": "e.g. every 1h or daily at 03:00",
      "proposedChange": "Concrete change: new frequency, removed tools, revised instruction, or 'no change needed'.",
      "rationale": "1–3 sentences explaining the evidence behind this recommendation."
    }
  ],
  "approvalInsights": [
    {
      "priority": "high|medium|low",
      "hostname": "hostname or 'all'",
      "pattern": "One sentence describing the observed approval/rejection pattern.",
      "recommendation": "Specific actionable change: automate this action type, retune the triggering heuristic, adjust risk classification, add a remediation skill, change notification strategy, etc.",
      "evidence": "Concrete data points from the approval history that support this finding (counts, risk levels, reason text, retry counts, timeout rates)."
    }
  ]
}

Rules:
- Maximum 9 entries in each array, ordered high → low priority.
- Omit approvalInsights (or set to []) if no approval history was provided.
- Omit resourceOptimization (or set to []) if no token/responsibility data was provided.
- Only include a finding if there is real evidence in the data.
- "feature" category = a new agent skill or central-backend capability to build.
- Headline must not repeat any recommendation title verbatim.
- For resourceOptimization: a responsibility that consistently reports "nothing found" on a short cycle is a strong candidate for a longer interval. A responsibility with high token use but thin summaries may need instruction tightening.
- For approvalInsights: cite specific counts, risk levels, and operator reason text where available. Do not fabricate patterns.`;

async function analyse() {
  const agents = getFleet();
  if (!agents) {
    logger.info('intelligence: fleet data not yet available, skipping');
    return;
  }

  logger.info(`intelligence: analysing fleet (${agents.length} agents)`);

  const fleetText = serializeFleet(agents);

  // Gather gateway stats and per-agent responsibility + memory in parallel.
  const [gatewayStats, agentDetails] = await Promise.all([
    getStats().catch((err) => {
      logger.warn(`intelligence: failed to fetch gateway stats: ${err.message}`);
      return null;
    }),
    Promise.all(
      agents.map(async (a) => {
        const [responsibilities, memory] = await Promise.all([
          fleet.getAgentResponsibilities(a.id).catch(() => null),
          fleet.getAgentMemory(a.id).catch(() => null),
        ]);
        return { hostname: a.hostname, responsibilities, memory };
      })
    ),
  ]);

  const tokenText    = serializeTokenStats(gatewayStats, agentDetails);
  const approvalText = serializeApprovalHistory(agentDetails);

  const sections = [fleetText, tokenText, approvalText].filter(Boolean);
  const userContent = sections.join('\n\n');

  let raw;
  try {
    const response = await _client.chat.completions.create({
      model: config.intelligence.model,
      max_tokens: 4096,
      messages: [
        { role: 'system', content: SYSTEM_PROMPT },
        { role: 'user', content: userContent },
      ],
    });

    if (response?.error) {
      logger.error(`intelligence: LLM error body`, { error: response.error });
      return;
    }

    raw = response?.choices?.[0]?.message?.content?.trim() ?? '';
    if (!raw) {
      logger.error(`intelligence: empty LLM response`);
      return;
    }
  } catch (err) {
    logger.error(`intelligence: API call failed: ${err.message}`);
    return;
  }

  let parsed;
  try {
    const cleaned = raw.replace(/^```(?:json)?\n?/, '').replace(/\n?```$/, '').trim();
    parsed = JSON.parse(cleaned);
  } catch (err) {
    logger.error(`intelligence: failed to parse response: ${err.message}\n${raw}`);
    return;
  }

  const report = {
    headline: parsed.headline ?? '',
    recommendations: Array.isArray(parsed.recommendations) ? parsed.recommendations : [],
    resourceOptimization: Array.isArray(parsed.resourceOptimization) ? parsed.resourceOptimization : [],
    approvalInsights: Array.isArray(parsed.approvalInsights) ? parsed.approvalInsights : [],
    generatedAt: new Date().toISOString(),
    agentCount: agents.length,
  };

  setReport(report);
  logger.info(
    `intelligence: report ready — ${report.recommendations.length} recommendation(s), ` +
    `${report.resourceOptimization.length} resource optimization(s), ` +
    `${report.approvalInsights.length} approval insight(s)`
  );
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

export function start() {
  if (!config.intelligence.baseUrl) {
    logger.info('intelligence: INTELLIGENCE_BASE_URL not set — fleet intelligence disabled');
    return;
  }

  _client = new OpenAI({
    apiKey: config.intelligence.apiKey,
    baseURL: config.intelligence.baseUrl,
    timeout: config.intelligence.timeoutMs,
    maxRetries: 0,
  });

  // First run deferred 5 min so the initial poll cycle and gateway can settle.
  setTimeout(analyse, 5 * 60 * 1000);

  const intervalMs = config.intelligence.intervalMinutes * 60 * 1000;
  if (intervalMs > 0) {
    _timer = setInterval(analyse, intervalMs);
    logger.info(
      `intelligence: started — first run in 5m, then every ${config.intelligence.intervalMinutes}m (model: ${config.intelligence.model})`
    );
  } else {
    logger.info(`intelligence: started — single run in 5m (model: ${config.intelligence.model})`);
  }
}

export function stop() {
  if (_timer) {
    clearInterval(_timer);
    _timer = null;
  }
}
