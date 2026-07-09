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

// buildReportParams constructs the query string for report endpoints.
// If filters.start and filters.end are set (ISO datetime-local strings), they are
// converted to unix timestamps and sent as start/end; otherwise range is used.
function buildReportParams(range, limit, filters = {}) {
  const params = new URLSearchParams();
  if (filters.start && filters.end) {
    const s = Math.floor(new Date(filters.start).getTime() / 1000);
    const e = Math.floor(new Date(filters.end).getTime() / 1000);
    if (!isNaN(s) && !isNaN(e) && e > s) {
      params.set('start', s);
      params.set('end', e);
    } else {
      params.set('range', range);
    }
  } else {
    params.set('range', range);
  }
  if (limit != null) params.set('limit', limit);
  if (filters.hostname) params.set('hostname', filters.hostname);
  if (filters.lab) params.set('lab', filters.lab);
  return params.toString();
}

export const getTopAppsByLaunches = (range = '24h', limit = 10, filters = {}) =>
  request(`/reports/top-apps-by-launches?${buildReportParams(range, limit, filters)}`);
export const getTopAppsByUsage = (range = '24h', limit = 10, filters = {}) =>
  request(`/reports/top-apps?${buildReportParams(range, limit, filters)}`);
export const getTopAppsByForeground = (range = '24h', limit = 10, filters = {}) =>
  request(`/reports/top-apps-by-foreground?${buildReportParams(range, limit, filters)}`);
export const getBottomAppsByLaunches = (range = '24h', limit = 10, filters = {}) =>
  request(`/reports/bottom-apps-by-launches?${buildReportParams(range, limit, filters)}`);
export const getBottomAppsByForeground = (range = '24h', limit = 10, filters = {}) =>
  request(`/reports/bottom-apps-by-foreground?${buildReportParams(range, limit, filters)}`);
export const getUsageByLab = (range = '24h', filters = {}) =>
  request(`/reports/usage-by-lab?${buildReportParams(range, null, filters)}`);
export const getActiveUsers = () => request('/reports/active-users');
export const ignoreApp = (name) => request('/reports/ignore-app', { method: 'POST', body: JSON.stringify({ name }) });
export const getTopDevicesBySessions = (range = '24h', limit = 10, filters = {}) =>
  request(`/reports/top-devices-by-sessions?${buildReportParams(range, limit, filters)}`);
export const getTopUsersByLogins = (range = '24h', limit = 10, filters = {}) =>
  request(`/reports/top-users-by-logins?${buildReportParams(range, limit, filters)}`);
export const getTopUsersBySessionTime = (range = '24h', limit = 10, filters = {}) =>
  request(`/reports/top-users-by-session-time?${buildReportParams(range, limit, filters)}`);
export const getAvgSessionTime = (range = '24h', limit = 10, filters = {}) =>
  request(`/reports/avg-session-time?${buildReportParams(range, limit, filters)}`);

// Parse a Prometheus instant-query vector response into [{name, category, value}]
// nameLabel controls which metric label becomes the display name (default: 'app').
export function parsePromVector(res, nameLabel = 'app') {
  if (!res?.data?.result) return [];
  const rows = res.data.result.map(r => ({
    name: r.metric?.[nameLabel] ?? r.metric?.app ?? r.metric?.user ?? r.metric?.hostname ?? r.metric?.__name__ ?? 'unknown',
    category: r.metric?.category ?? '',
    lab: r.metric?.lab ?? '',
    value: parseFloat(r.value?.[1] ?? 0),
  }));

  // Deduplicate: the same app can appear multiple times with different category
  // labels because Prometheus retains old time series after a mapping change.
  // Merge by (name, lab): sum values, keep the category from the highest entry.
  const key = r => `${r.name}\0${r.lab}`;
  const merged = new Map();
  for (const r of rows) {
    const k = key(r);
    if (!merged.has(k)) {
      merged.set(k, { ...r });
    } else {
      const m = merged.get(k);
      if (r.value > m.value) {
        merged.set(k, { ...r, value: m.value + r.value });
      } else {
        m.value += r.value;
      }
    }
  }

  return [...merged.values()]
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

export const exportTopAppsByLaunches = (range = '24h', filters = {}) =>
  downloadCSV(`/reports/top-apps-by-launches?${buildReportParams(range, null, filters)}&format=csv`, `top-apps-by-launches.csv`);

export const exportTopAppsByForeground = (range = '24h', filters = {}) =>
  downloadCSV(`/reports/top-apps?${buildReportParams(range, null, filters)}&format=csv`, `top-apps-by-active-time.csv`);

export const exportBottomAppsByLaunches = (range = '24h', filters = {}) =>
  downloadCSV(`/reports/bottom-apps-by-launches?${buildReportParams(range, null, filters)}&format=csv`, `bottom-apps-by-launches.csv`);

export const exportBottomAppsByForeground = (range = '24h', filters = {}) =>
  downloadCSV(`/reports/bottom-apps-by-foreground?${buildReportParams(range, null, filters)}&format=csv`, `bottom-apps-by-foreground.csv`);

// Installers
export const generateInstaller = (data) =>
  request('/installers/generate', { method: 'POST', body: JSON.stringify(data) });
export const getMacInstallerURL = () => `${BASE}/installers/latest?platform=mac`;

// Settings
export const getSettings = () => request('/settings');
export const updateSettings = (data) => request('/settings', { method: 'PUT', body: JSON.stringify(data) });
