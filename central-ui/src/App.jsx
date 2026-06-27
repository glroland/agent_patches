import { Routes, Route } from 'react-router-dom';
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
import { FleetSocketProvider } from './hooks/useFleetSocket';
import { ChatHistoryProvider } from './hooks/useChatHistory';

export default function App() {
  return (
    <ChatHistoryProvider>
    <FleetSocketProvider>
      <Routes>
        <Route element={<Layout />}>
          <Route path="/" element={<Dashboard />} />
          <Route path="/agents" element={<Agents />} />
          <Route path="/agents/:id" element={<AgentDetail />} />
          <Route path="/approvals" element={<Approvals />} />
          <Route path="/issues" element={<Issues />} />
          <Route path="/chat" element={<FleetChat />} />
          <Route path="/intelligence" element={<FleetIntelligence />} />
          <Route path="/activity" element={<ActivityFeed />} />
          <Route path="/statistics" element={<Statistics />} />
          <Route path="/admin" element={<Admin />} />
        </Route>
      </Routes>
    </FleetSocketProvider>
    </ChatHistoryProvider>
  );
}
