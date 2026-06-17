import { createContext, useContext, useEffect, useRef, useState } from 'react';

const API_BASE = import.meta.env.VITE_API_BASE_URL || 'http://localhost:4000/api';
const WS_URL = API_BASE.replace(/^http/, 'ws').replace(/\/api$/, '/ws');

const RECONNECT_DELAY_MS = 3000;

const FleetSocketContext = createContext(null);

export function FleetSocketProvider({ children }) {
  const [state, setState] = useState({
    agents: null,
    dashboard: null,
    summary: null,
    connected: false,
  });

  const wsRef = useRef(null);
  const reconnectTimer = useRef(null);
  const unmounted = useRef(false);

  useEffect(() => {
    unmounted.current = false;

    function connect() {
      if (unmounted.current) return;

      const ws = new WebSocket(WS_URL);
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
            setState({
              agents: msg.agents,
              dashboard: msg.dashboard,
              summary: msg.summary,
              connected: true,
            });
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
