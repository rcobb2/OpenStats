import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter, Routes, Route, Navigate, useLocation } from 'react-router-dom';
import Layout from './components/Layout';
import ErrorBoundary from './components/ErrorBoundary';
import Dashboard from './pages/Dashboard';
import AgentsList from './pages/agents/AgentsList';
import Installer from './pages/agents/Installer';
import Settings from './pages/agents/Settings';
import Labs from './pages/Labs';
import Mappings from './pages/Mappings';
import Users from './pages/Users';
import Reports from './pages/Reports';
import './styles.css';

// Preserve ?range=/lab=/etc. when redirecting the bare /reports path to its
// default sub-route, so a deep link to a filtered view set before the
// report-type split still lands on the right data, not just the right page.
function ReportsIndexRedirect() {
  const location = useLocation();
  return <Navigate to={{ pathname: '/reports/user', search: location.search }} replace />;
}

ReactDOM.createRoot(document.getElementById('root')).render(
  <React.StrictMode>
    <BrowserRouter>
      <ErrorBoundary>
        <Routes>
          <Route element={<Layout />}>
            <Route path="/" element={<Dashboard />} />
            <Route path="/agents" element={<AgentsList />} />
            <Route path="/agents/installers" element={<Installer />} />
            <Route path="/agents/settings" element={<Settings />} />
            <Route path="/labs" element={<Labs />} />
            <Route path="/mappings" element={<Mappings />} />
            <Route path="/users" element={<Users />} />
            <Route path="/reports" element={<ReportsIndexRedirect />} />
            <Route path="/reports/:type" element={<Reports />} />
            {/* Redirect old installer path */}
            <Route path="/installer" element={<Navigate to="/agents/installers" replace />} />
          </Route>
        </Routes>
      </ErrorBoundary>
    </BrowserRouter>
  </React.StrictMode>
);
