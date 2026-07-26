// Drives email notifications from the polling loop.
//
// Anti-spam rules:
//   - Critical issues: one email when first detected; no repeat until the
//     issue resolves and then reappears.
//   - Pending approvals: one email (batched) when new approvals appear;
//     no repeat for the same approval ID.
//   - Escalation reminders: one follow-up email per approval, once it has
//     been pending past the halfway point of its timeout window.
//   - Daily summary: once per day at the configured time.
//
// State is in-memory. A restart causes one re-notification for any still-open
// issues, which is acceptable (it's one email, not a flood).

import { logger } from '../utils/logger.js';
import { concerns, pendingApprovals, recentActivity } from './activity.js';
import { sendEmail } from './emailer.js';

let _cfg = null;

// Keys we have already sent email for. Prefixed to distinguish types:
//   "issue:<concern.id>"      — critical concern
//   "approval:<entry.id>"     — pending approval
//   "escalate:<entry.id>"     — approval escalation reminder
const _notified = new Set();

// Mirrors endpoint-server/skills/request_approval's approval timeout: 24h by
// default, 48h when the action itself is high risk (e.g. a patch requiring a
// reboot). Escalate at the halfway point so an operator who missed the first
// email gets a second nudge before the approval auto-cancels.
const APPROVAL_TIMEOUT_HOURS = { high: 48, default: 24 };

// YYYY-MM-DD string of the last day a daily summary was sent (or attempted).
let _lastDailySummaryDate = null;

export function start(emailConfig) {
  _cfg = emailConfig;
  if (!_cfg.enabled) {
    logger.info('notifier: email disabled, no notifications will be sent');
    return;
  }
  logger.info(`notifier: started (daily summary at ${_cfg.dailySummaryTime})`);
}

// Called by the poller after each successful fleet update.
export async function onFleetUpdate(rawAgents) {
  if (!_cfg?.enabled) return;
  await _checkCriticalConcerns(rawAgents);
  await _checkPendingApprovals(rawAgents);
  await _checkEscalations(rawAgents);
  await _maybeSendDailySummary(rawAgents);
}

// ---------------------------------------------------------------------------
// Critical concerns
// ---------------------------------------------------------------------------

async function _checkCriticalConcerns(rawAgents) {
  const criticals = concerns(rawAgents).filter((c) => c.severity === 'critical');
  const liveKeys = new Set(criticals.map((c) => `issue:${c.id}`));

  // Drop resolved issues so a recurrence would re-notify.
  for (const key of [..._notified]) {
    if (key.startsWith('issue:') && !liveKeys.has(key)) {
      _notified.delete(key);
    }
  }

  for (const concern of criticals) {
    const key = `issue:${concern.id}`;
    if (_notified.has(key)) continue;

    const subject = `[agent_patches] URGENT: ${concern.title} — ${concern.hostname}`;
    const lines = [
      `A critical issue has been detected on ${concern.hostname}.`,
      '',
      `Issue:  ${concern.title}`,
    ];
    if (concern.detail) lines.push(`Detail: ${concern.detail}`);
    if (concern.time) lines.push(`Time:   ${new Date(concern.time).toLocaleString()}`);
    lines.push('', 'Log in to the agent_patches console to investigate.');
    const body = lines.join('\n');

    try {
      await sendEmail(_cfg, subject, body);
      _notified.add(key);
    } catch (err) {
      logger.error(`notifier: failed to send critical concern email: ${err.message}`);
      // Do not add to _notified — retry on next poll.
    }
  }
}

// ---------------------------------------------------------------------------
// Pending approvals
// ---------------------------------------------------------------------------

async function _checkPendingApprovals(rawAgents) {
  const approvals = pendingApprovals(rawAgents);
  const liveKeys = new Set(approvals.map((a) => `approval:${a.id}`));

  // Drop resolved approvals so they don't accumulate in the set.
  for (const key of [..._notified]) {
    if (key.startsWith('approval:') && !liveKeys.has(key)) {
      _notified.delete(key);
    }
  }

  const fresh = approvals.filter((a) => !_notified.has(`approval:${a.id}`));
  if (fresh.length === 0) return;

  const subject =
    fresh.length === 1
      ? `[agent_patches] Approval needed: ${fresh[0].title}`
      : `[agent_patches] ${fresh.length} approvals need your review`;

  const lines = [`${fresh.length} action(s) require your approval:`, ''];
  for (const [i, a] of fresh.entries()) {
    lines.push(`${i + 1}. [${a.risk?.toUpperCase() ?? 'UNKNOWN'} RISK] ${a.hostname}`);
    lines.push(`   ${a.title}`);
    if (a.detail) lines.push(`   ${a.detail}`);
    if (a.proposedAction) lines.push(`   Proposed: ${a.proposedAction}`);
    if (i < fresh.length - 1) lines.push('');
  }
  lines.push('', 'Review and approve or reject at the agent_patches console.');
  const body = lines.join('\n');

  try {
    await sendEmail(_cfg, subject, body);
    for (const a of fresh) _notified.add(`approval:${a.id}`);
  } catch (err) {
    logger.error(`notifier: failed to send approval email: ${err.message}`);
    // Do not add to _notified — retry on next poll.
  }
}

// ---------------------------------------------------------------------------
// Escalation reminders
// ---------------------------------------------------------------------------

async function _checkEscalations(rawAgents) {
  const approvals = pendingApprovals(rawAgents);
  const liveKeys = new Set(approvals.map((a) => `escalate:${a.id}`));

  // Drop resolved approvals so a future one reusing the same id (won't
  // happen in practice — ids are UUIDs — but mirrors _checkPendingApprovals)
  // can escalate again.
  for (const key of [..._notified]) {
    if (key.startsWith('escalate:') && !liveKeys.has(key)) {
      _notified.delete(key);
    }
  }

  const now = Date.now();
  for (const a of approvals) {
    const key = `escalate:${a.id}`;
    if (_notified.has(key)) continue;
    // Only high-importance/high-risk approvals get an initial email at all
    // (see _checkPendingApprovals' upstream gate on the agent side) — mirror
    // that here so low-stakes approvals don't get a reminder either.
    if (a.importance !== 'high' && a.risk !== 'high') continue;

    const timeoutHours = a.risk === 'high' ? APPROVAL_TIMEOUT_HOURS.high : APPROVAL_TIMEOUT_HOURS.default;
    const ageHours = (now - new Date(a.time).getTime()) / 3_600_000;
    if (ageHours < timeoutHours / 2) continue;

    const subject = `[agent_patches] REMINDER: approval still pending — ${a.hostname}`;
    const lines = [
      `An approval has been awaiting your decision for over ${Math.floor(ageHours)}h.`,
      '',
      `Host:     ${a.hostname}`,
      `[${(a.risk ?? 'unknown').toUpperCase()} RISK] ${a.title}`,
    ];
    if (a.detail) lines.push(`Detail:   ${a.detail}`);
    if (a.proposedAction) lines.push(`Proposed: ${a.proposedAction}`);
    lines.push('', `It will be automatically cancelled ${timeoutHours}h after the original request if no decision is made.`);
    lines.push('Review and approve or reject at the agent_patches console.');
    const body = lines.join('\n');

    try {
      await sendEmail(_cfg, subject, body);
      _notified.add(key);
    } catch (err) {
      logger.error(`notifier: failed to send escalation email: ${err.message}`);
      // Do not add to _notified — retry on next poll.
    }
  }
}

// ---------------------------------------------------------------------------
// Daily summary
// ---------------------------------------------------------------------------

async function _maybeSendDailySummary(rawAgents) {
  const now = new Date();
  const todayStr = now.toISOString().slice(0, 10);

  // Already sent (or attempted) today.
  if (_lastDailySummaryDate === todayStr) return;

  // Not yet reached the configured send time.
  const [targetH, targetM] = _cfg.dailySummaryTime.split(':').map(Number);
  if (now.getHours() < targetH || (now.getHours() === targetH && now.getMinutes() < targetM)) return;

  // Mark as sent before attempting — if SMTP fails we don't want to retry on
  // every subsequent poll for the rest of the day.
  _lastDailySummaryDate = todayStr;

  const attentionAgents = rawAgents.filter((a) => ['attention', 'offline'].includes(a.status));
  const approvals = pendingApprovals(rawAgents);
  const activity = recentActivity(rawAgents, 10);

  const lines = [
    `Agent Patches — Daily Fleet Summary`,
    `${todayStr}`,
    '='.repeat(40),
    '',
    `Total agents:      ${rawAgents.length}`,
    `Healthy:           ${rawAgents.length - attentionAgents.length}`,
    `Needs attention:   ${attentionAgents.length}`,
    `Pending approvals: ${approvals.length}`,
    '',
  ];

  if (attentionAgents.length > 0) {
    lines.push('AGENTS NEEDING ATTENTION:');
    for (const a of attentionAgents) {
      lines.push(`  • ${a.hostname} (${a.statusLabel})`);
      if (a.currentTask) lines.push(`    ${a.currentTask}`);
    }
    lines.push('');
  }

  if (approvals.length > 0) {
    lines.push('PENDING APPROVALS:');
    for (const a of approvals) {
      lines.push(`  • [${a.risk?.toUpperCase() ?? 'UNKNOWN'}] ${a.hostname}: ${a.title}`);
    }
    lines.push('');
  }

  if (activity.length > 0) {
    lines.push('RECENT ACTIVITY:');
    for (const e of activity) {
      const ts = e.time ? new Date(e.time).toLocaleTimeString() : '';
      lines.push(`  • ${e.hostname}${ts ? ' @ ' + ts : ''}: ${e.title}`);
    }
    lines.push('');
  }

  const subject = `[agent_patches] Daily fleet summary — ${todayStr}`;
  const body = lines.join('\n');

  try {
    await sendEmail(_cfg, subject, body);
    logger.info('notifier: daily summary sent');
  } catch (err) {
    logger.error(`notifier: failed to send daily summary: ${err.message}`);
  }
}
