import { createContext, useCallback, useContext, useEffect, useState } from 'react';
import { getToken, storeToken, clearToken } from './oauth.js';

const AuthContext = createContext(null);

export function AuthProvider({ children }) {
  const [token,       setTokenState] = useState(() => getToken());
  const [oauthConfig, setOauthConfig] = useState(null);
  const [loading,     setLoading]     = useState(true);

  // Fetched once on mount — unauthed endpoint that returns { clientId, authorizeUrl }.
  useEffect(() => {
    fetch('/api/auth/config')
      .then((r) => r.json())
      .then((cfg) => setOauthConfig(cfg))
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  const setToken = useCallback((t) => {
    storeToken(t);
    setTokenState(t);
  }, []);

  const logout = useCallback(() => {
    clearToken();
    setTokenState(null);
  }, []);

  return (
    <AuthContext.Provider value={{ token, setToken, logout, oauthConfig, loading }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  return useContext(AuthContext);
}
