import { createContext, useContext, useEffect, useRef, useState } from 'react';
import { getToken } from '../auth/oauth.js';

const RECONNECT_DELAY_MS = 3000;

const FleetSocketContext = createContext(null);

function buildWsUrl() {
  const base  = `${window.location.origin.replace(/^http/, 'ws')}/ws`;
  const token = getToken();
  return token ? `${base}?token=${encodeURIComponent(token)}` : base;
}

export function FleetSocketProvider({ children }) {
  const [state, setState] = useState({
    agents: null,
    dashboard: null,
    summary: null,
    intelligence: null,
    intelligenceStatus: null,
    briefing: null,
    connected: false,
  });

  const wsRef           = useRef(null);
  const reconnectTimer  = useRef(null);
  const unmounted       = useRef(false);

  useEffect(() => {
    unmounted.current = false;

    function connect() {
      if (unmounted.current) return;

      const ws = new WebSocket(buildWsUrl());
      wsRef.current = ws;

      ws.onopen = () => {
        if (unmounted.current) return;
        setState((s) => ({ ...s, connected: true }));
      };

      ws.onmessage = (event) => {
        if (unmounted.current) return;
        try {
          const msg = JSON.parse(event.data);
          if (msg.type === 'fleet_update') {
            setState((prev) => ({
              agents:       msg.agents,
              dashboard:    msg.dashboard,
              summary:      msg.summary,
              intelligence:       msg.intelligence       !== undefined ? msg.intelligence       : prev.intelligence,
              intelligenceStatus: msg.intelligenceStatus !== undefined ? msg.intelligenceStatus : prev.intelligenceStatus,
              briefing:           msg.briefing           !== undefined ? msg.briefing           : prev.briefing,
              connected:    true,
            }));
          }
        } catch {
          // ignore malformed messages
        }
      };

      ws.onclose = () => {
        if (unmounted.current) return;
        setState((s) => ({ ...s, connected: false }));
        reconnectTimer.current = setTimeout(connect, RECONNECT_DELAY_MS);
      };

      ws.onerror = () => {
        ws.close();
      };
    }

    connect();

    return () => {
      unmounted.current = true;
      clearTimeout(reconnectTimer.current);
      wsRef.current?.close();
    };
  }, []);

  return (
    <FleetSocketContext.Provider value={state}>
      {children}
    </FleetSocketContext.Provider>
  );
}

export function useFleetSocket() {
  return useContext(FleetSocketContext);
}
