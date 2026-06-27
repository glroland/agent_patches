import { Outlet, Routes, Route } from 'react-router-dom';
import { AuthProvider, useAuth } from './auth/AuthContext.jsx';
import { startLogin } from './auth/oauth.js';
import Layout from './components/Layout';
import Dashboard from './pages/Dashboard';
import Agents from './pages/Agents';
import AgentDetail from './pages/AgentDetail';
import Approvals from './pages/Approvals';
import Issues from './pages/Issues';
import FleetChat from './pages/FleetChat';
import FleetIntelligence from './pages/FleetIntelligence';
import ActivityFeed from './pages/ActivityFeed';
import Admin from './pages/Admin';
import Statistics from './pages/Statistics';
import OAuthCallback from './pages/OAuthCallback';
import { FleetSocketProvider } from './hooks/useFleetSocket';
import { ChatHistoryProvider } from './hooks/useChatHistory';

// Guards all authenticated routes. Redirects to OpenShift OAuth if no token.
// Also wraps providers that require an authenticated token (WebSocket, chat).
function AuthGuard() {
  const { token, oauthConfig, loading } = useAuth();

  if (loading) return null;

  if (!token) {
    if (oauthConfig) startLogin(oauthConfig);
    return null;
  }

  return (
    <ChatHistoryProvider>
      <FleetSocketProvider>
        <Outlet />
      </FleetSocketProvider>
    </ChatHistoryProvider>
  );
}

export default function App() {
  return (
    <AuthProvider>
      <Routes>
        {/* Public route — no auth required */}
        <Route path="/oauth/callback" element={<OAuthCallback />} />

        {/* All authenticated routes */}
        <Route element={<AuthGuard />}>
          <Route element={<Layout />}>
            <Route path="/"            element={<Dashboard />} />
            <Route path="/agents"      element={<Agents />} />
            <Route path="/agents/:id"  element={<AgentDetail />} />
            <Route path="/approvals"   element={<Approvals />} />
            <Route path="/issues"      element={<Issues />} />
            <Route path="/chat"        element={<FleetChat />} />
            <Route path="/intelligence" element={<FleetIntelligence />} />
            <Route path="/activity"    element={<ActivityFeed />} />
            <Route path="/statistics"  element={<Statistics />} />
            <Route path="/admin"       element={<Admin />} />
          </Route>
        </Route>
      </Routes>
    </AuthProvider>
  );
}
