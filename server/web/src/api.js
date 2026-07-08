const BASE = '/api/v1';

async function request(path, options = {}) {
  const res = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json', ...options.headers },
    ...options,
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }));
    const err = new Error(body.error || res.statusText);
    err.status = res.status;
    err.body = body;
    throw err;
  }
  return res.json();
}

// Agents
export const getAgents = () => request('/agents');
export const getAgent = (id) => request(`/agents/${id}`);
export const deleteAgent = (id) => request(`/agents/${id}`, { method: 'DELETE' });
export const forceAgentUpdate = (id) => request(`/agents/${id}/force-update`, { method: 'POST' });
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
export const patchMappingIgnore = (id, ignored) => request(`/mappings/${id}/ignore`, { method: 'PATCH', body: JSON.stringify({ ignored }) });

// Reports
export const getSummary = () => request('/reports/summary');
export const getTopAppsByLaunches = (range = '24h', limit = 10) => request(`/reports/top-apps-by-launches?range=${range}&limit=${limit}`);
export const getTopAppsByForeground = (range = '24h', limit = 10) => request(`/reports/top-apps-by-foreground?range=${range}&limit=${limit}`);
export const getBottomAppsByLaunches = (range = '24h', limit = 10) => request(`/reports/bottom-apps-by-launches?range=${range}&limit=${limit}`);
export const getBottomAppsByForeground = (range = '24h', limit = 10) => request(`/reports/bottom-apps-by-foreground?range=${range}&limit=${limit}`);
export const getUsageByLab = (range = '24h') => request(`/reports/usage-by-lab?range=${range}`);
export const getActiveUsers = () => request('/reports/active-users');

// Parse a Prometheus instant-query vector response into [{name, category, value}]
export function parsePromVector(res) {
  if (!res?.data?.result) return [];
  return res.data.result
    .map(r => ({
      name: r.metric?.app ?? r.metric?.__name__ ?? 'unknown',
      category: r.metric?.category ?? '',
      lab: r.metric?.lab ?? '',
      value: parseFloat(r.value?.[1] ?? 0),
    }))
    .filter(r => r.value > 0)
    .sort((a, b) => b.value - a.value);
}

// CSV Export helpers
async function downloadCSV(path, filename) {
  const baseUrl = window.location.origin;
  const res = await fetch(`${baseUrl}${BASE}${path}`, {
    headers: { 'Accept': 'text/csv' },
  });
  if (!res.ok) throw new Error('Failed to download');
  const blob = await res.blob();
  const url = window.URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  try {
    a.click();
  } finally {
    document.body.removeChild(a);
    window.URL.revokeObjectURL(url);
  }
}

export const exportTopAppsByLaunches = (range = '24h') => 
  downloadCSV(`/reports/top-apps-by-launches?range=${range}&format=csv`, `top-apps-by-launches-${range}.csv`);

export const exportTopAppsByForeground = (range = '24h') => 
  downloadCSV(`/reports/top-apps-by-foreground?range=${range}&format=csv`, `top-apps-by-foreground-${range}.csv`);

export const exportBottomAppsByLaunches = (range = '24h') => 
  downloadCSV(`/reports/bottom-apps-by-launches?range=${range}&format=csv`, `bottom-apps-by-launches-${range}.csv`);

export const exportBottomAppsByForeground = (range = '24h') => 
  downloadCSV(`/reports/bottom-apps-by-foreground?range=${range}&format=csv`, `bottom-apps-by-foreground-${range}.csv`);

// Installers
export const generateInstaller = (data) =>
  request('/installers/generate', { method: 'POST', body: JSON.stringify(data) });
export const getMacInstallerURL = () => `${BASE}/installers/latest?platform=mac`;

// Settings
export const getSettings = () => request('/settings');
export const updateSettings = (data) => request('/settings', { method: 'PUT', body: JSON.stringify(data) });
