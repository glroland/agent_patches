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
