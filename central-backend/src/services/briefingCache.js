// In-memory cache for the operator briefing.
// Short TTL; invalidate() forces regeneration on the next fleet update.

export const BRIEFING_TTL_MS = 5 * 60 * 1000; // 5 minutes

let _briefing = null;
let _generatedAt = 0;
const _listeners = new Set();

export function getBriefing() {
  return _briefing;
}

export function setBriefing(briefing) {
  _briefing = briefing;
  _generatedAt = Date.now();
  for (const fn of _listeners) fn(briefing);
}

export function invalidate() {
  _generatedAt = 0;
}

export function isStale() {
  return Date.now() - _generatedAt > BRIEFING_TTL_MS;
}

export function subscribe(fn) {
  _listeners.add(fn);
  return () => _listeners.delete(fn);
}
