import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter, Routes, Route } from 'react-router-dom';
import Layout from './components/Layout';
import Dashboard from './pages/Dashboard';
import Agents from './pages/Agents';
import Labs from './pages/Labs';
import Mappings from './pages/Mappings';
import Installer from './pages/Installer';
import Reports from './pages/Reports';
import './styles.css';

ReactDOM.createRoot(document.getElementById('root')).render(
  <React.StrictMode>
    <BrowserRouter>
      <Routes>
        <Route element={<Layout />}>
          <Route path="/" element={<Dashboard />} />
          <Route path="/agents" element={<Agents />} />
          <Route path="/labs" element={<Labs />} />
          <Route path="/mappings" element={<Mappings />} />
          <Route path="/installer" element={<Installer />} />
          <Route path="/reports" element={<Reports />} />
        </Route>
      </Routes>
    </BrowserRouter>
  </React.StrictMode>
);
