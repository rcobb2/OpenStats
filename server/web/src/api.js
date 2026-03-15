const BASE = '/api/v1';

async function request(path, options = {}) {
  const res = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json', ...options.headers },
    ...options,
  });
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(err.error || res.statusText);
  }
  return res.json();
}

// Agents
export const getAgents = () => request('/agents');
export const getAgent = (id) => request(`/agents/${id}`);
export const deleteAgent = (id) => request(`/agents/${id}`, { method: 'DELETE' });
export const assignAgentToLab = (agentId, labId) =>
  request(`/agents/${agentId}/lab`, { method: 'PUT', body: JSON.stringify({ labId }) });

// Labs
export const getLabs = () => request('/labs');
export const createLab = (data) => request('/labs', { method: 'POST', body: JSON.stringify(data) });
export const updateLab = (id, data) => request(`/labs/${id}`, { method: 'PUT', body: JSON.stringify(data) });
export const deleteLab = (id) => request(`/labs/${id}`, { method: 'DELETE' });

// Mappings
export const getMappings = () => request('/mappings');
export const createMapping = (data) => request('/mappings', { method: 'POST', body: JSON.stringify(data) });
export const updateMapping = (data) => request('/mappings', { method: 'PUT', body: JSON.stringify(data) });
export const deleteMapping = (id) => request(`/mappings/${id}`, { method: 'DELETE' });

// Reports
export const getSummary = () => request('/reports/summary');
export const getTopApps = (range = '24h') => request(`/reports/top-apps?range=${range}`);
export const getUsageByLab = (range = '24h') => request(`/reports/usage-by-lab?range=${range}`);
export const getActiveUsers = () => request('/reports/active-users');

// Installers
export const generateInstaller = (data) =>
  request('/installers/generate', { method: 'POST', body: JSON.stringify(data) });

// Settings
export const getSettings = () => request('/settings');
export const updateSettings = (data) => request('/settings', { method: 'PUT', body: JSON.stringify(data) });
