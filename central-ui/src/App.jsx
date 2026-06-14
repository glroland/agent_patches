import { Routes, Route } from 'react-router-dom';
import Layout from './components/Layout';
import Dashboard from './pages/Dashboard';
import Endpoints from './pages/Endpoints';
import EndpointDetail from './pages/EndpointDetail';
import Issues from './pages/Issues';

export default function App() {
  return (
    <Routes>
      <Route element={<Layout />}>
        <Route path="/" element={<Dashboard />} />
        <Route path="/endpoints" element={<Endpoints />} />
        <Route path="/endpoints/:id" element={<EndpointDetail />} />
        <Route path="/issues" element={<Issues />} />
      </Route>
    </Routes>
  );
}
