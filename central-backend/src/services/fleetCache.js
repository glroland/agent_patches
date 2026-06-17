let _fleet = null;
const _listeners = new Set();

export function getFleet() {
  return _fleet;
}

export function setFleet(agents) {
  _fleet = agents;
  for (const fn of _listeners) {
    fn(agents);
  }
}

export function subscribe(fn) {
  _listeners.add(fn);
  return () => _listeners.delete(fn);
}
