// In-memory cache for the latest fleet intelligence report.
// Subscribers are notified whenever a new report is stored.

let _report = null;
const _listeners = new Set();

export function getReport() {
  return _report;
}

export function setReport(report) {
  _report = report;
  for (const fn of _listeners) {
    fn(report);
  }
}

export function subscribe(fn) {
  _listeners.add(fn);
  return () => _listeners.delete(fn);
}

export function clear() {
  _report = null;
  for (const fn of _listeners) {
    fn(null);
  }
}

// Tracks whether an analysis run is currently in flight and the outcome of
// the last one, so the UI can reflect "analysing" / "last run failed" state
// that survives a page refresh instead of only living in component state.
let _status = { running: false, lastError: null };
const _statusListeners = new Set();

export function getStatus() {
  return _status;
}

export function setStatus(status) {
  _status = status;
  for (const fn of _statusListeners) {
    fn(_status);
  }
}

export function subscribeStatus(fn) {
  _statusListeners.add(fn);
  return () => _statusListeners.delete(fn);
}
