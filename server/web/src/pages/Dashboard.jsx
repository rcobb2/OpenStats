import { useState, useEffect } from 'react';
import {
  BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer, Cell,
} from 'recharts';
import {
  getSummary, getTopAppsByLaunches, getActiveUsers, getUsageByLab,
  getTopAppsByElevations, getAgents, getLabs, parsePromVector,
} from '../api';
import { useGlobalFilters } from '../hooks/useGlobalFilters';
import GlobalFilterBar from '../components/GlobalFilterBar';

const CHART_COLORS = [
  'var(--accent)', 'var(--success)', 'var(--warning)', 'var(--danger)', '#a78bfa',
  '#34d399', '#fb923c', '#60a5fa', '#f472b6', '#818cf8',
];

function TopAppsChart({ range, filters }) {
  const [data, setData] = useState(null);
  const [error, setError] = useState(null);

  useEffect(() => {
    setData(null);
    setError(null);
    getTopAppsByLaunches(range, 10, filters)
      .then(res => setData(parsePromVector(res)))
      .catch(e => setError(e.message));
  }, [range, filters]);

  if (error) return <div className="error" style={{ padding: '1rem' }}>Chart unavailable: {error}</div>;
  if (!data) return <div className="loading" style={{ padding: '1rem' }}>Loading chart…</div>;
  if (data.length === 0) return <div className="empty">No data for this period.</div>;

  return (
    <ResponsiveContainer width="100%" height={300}>
      <BarChart layout="vertical" data={data} margin={{ top: 4, right: 24, bottom: 4, left: 8 }}>
        <XAxis
          type="number"
          allowDecimals={false}
          tick={{ fill: 'var(--text-dim)', fontSize: 11 }}
          axisLine={{ stroke: 'var(--border)' }}
          tickLine={false}
          label={{ value: 'launches', position: 'insideBottomRight', offset: -4, fill: 'var(--text-dim)', fontSize: 11 }}
        />
        <YAxis
          type="category"
          dataKey="name"
          width={150}
          tick={{ fill: 'var(--text)', fontSize: 12 }}
          axisLine={false}
          tickLine={false}
        />
        <Tooltip
          cursor={{ fill: 'rgba(255,255,255,0.04)' }}
          contentStyle={{ background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 6, fontSize: 13 }}
          labelStyle={{ color: 'var(--text)' }}
          formatter={(v, _name, { payload }) => [
            // PromQL increase() extrapolates at the query-range edges, so a
            // real integer launch count can come back as e.g. 95.452 — round
            // for display; the underlying value isn't fractional.
            v.toLocaleString(undefined, { maximumFractionDigits: 0 }),
            payload.category || 'launches',
          ]}
        />
        <Bar dataKey="value" radius={[0, 4, 4, 0]} maxBarSize={22}>
          {data.map((_, i) => (
            <Cell key={i} fill={CHART_COLORS[i % CHART_COLORS.length]} />
          ))}
        </Bar>
      </BarChart>
    </ResponsiveContainer>
  );
}

// getUsageByLab returns per-(lab, app) foreground seconds as a Prometheus
// vector; this page only needs the per-lab total, so roll the apps up here.
function LabUsageChart({ range, filters }) {
  const [data, setData] = useState(null);
  const [error, setError] = useState(null);

  useEffect(() => {
    setData(null);
    setError(null);
    getUsageByLab(range, filters)
      .then(res => {
        const rows = parsePromVector(res);
        const byLab = new Map();
        for (const r of rows) {
          const lab = r.lab || 'Unassigned';
          byLab.set(lab, (byLab.get(lab) || 0) + r.value);
        }
        const totals = [...byLab.entries()]
          .map(([name, seconds]) => ({ name, value: seconds / 3600 }))
          .sort((a, b) => b.value - a.value);
        setData(totals);
      })
      .catch(e => setError(e.message));
  }, [range, filters]);

  if (error) return <div className="error" style={{ padding: '1rem' }}>Chart unavailable: {error}</div>;
  if (!data) return <div className="loading" style={{ padding: '1rem' }}>Loading chart…</div>;
  if (data.length === 0) return <div className="empty">No data for this period.</div>;

  return (
    <ResponsiveContainer width="100%" height={300}>
      <BarChart layout="vertical" data={data} margin={{ top: 4, right: 24, bottom: 4, left: 8 }}>
        <XAxis
          type="number"
          tick={{ fill: 'var(--text-dim)', fontSize: 11 }}
          axisLine={{ stroke: 'var(--border)' }}
          tickLine={false}
          label={{ value: 'hours', position: 'insideBottomRight', offset: -4, fill: 'var(--text-dim)', fontSize: 11 }}
        />
        <YAxis
          type="category"
          dataKey="name"
          width={150}
          tick={{ fill: 'var(--text)', fontSize: 12 }}
          axisLine={false}
          tickLine={false}
        />
        <Tooltip
          cursor={{ fill: 'rgba(255,255,255,0.04)' }}
          contentStyle={{ background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 6, fontSize: 13 }}
          labelStyle={{ color: 'var(--text)' }}
          formatter={(v) => [`${v.toLocaleString(undefined, { maximumFractionDigits: 1 })} hrs`, 'active time']}
        />
        <Bar dataKey="value" radius={[0, 4, 4, 0]} maxBarSize={22}>
          {data.map((_, i) => (
            <Cell key={i} fill={CHART_COLORS[i % CHART_COLORS.length]} />
          ))}
        </Bar>
      </BarChart>
    </ResponsiveContainer>
  );
}

function RecentElevationsPanel({ range, filters }) {
  const [data, setData] = useState(null);
  const [error, setError] = useState(null);

  useEffect(() => {
    setData(null);
    setError(null);
    getTopAppsByElevations(range, 6, filters)
      .then(res => setData(parsePromVector(res)))
      .catch(e => setError(e.message));
  }, [range, filters]);

  if (error) return <div className="error">Unavailable: {error}</div>;
  if (!data) return <div className="loading">Loading…</div>;
  if (data.length === 0) return <div className="empty">No privilege elevations in this period.</div>;

  const max = Math.max(...data.map(d => d.value));

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '0.65rem' }}>
      {data.map((d, i) => (
        <div key={d.name} style={{ display: 'flex', alignItems: 'center', gap: '0.65rem' }}>
          <span style={{
            width: 140, fontSize: 13, color: 'var(--text)',
            overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', flexShrink: 0,
          }}>
            {d.name}
          </span>
          <div style={{ flex: 1, height: 8, background: 'var(--surface-2)', borderRadius: 4, overflow: 'hidden' }}>
            <div style={{
              width: `${max > 0 ? (d.value / max) * 100 : 0}%`,
              height: '100%',
              background: CHART_COLORS[i % CHART_COLORS.length],
              borderRadius: 4,
            }} />
          </div>
          <span style={{ fontSize: 12, color: 'var(--text-dim)', width: 32, textAlign: 'right', flexShrink: 0 }}>
            {Math.round(d.value)}
          </span>
        </div>
      ))}
    </div>
  );
}

// Deliberately fleet-wide regardless of the page's lab scope selector — this
// is a triage panel ("what needs attention right now"), and narrowing it to
// one lab would hide an offline machine in a lab you forgot to filter out.
function FleetHealthPanel() {
  const [agents, setAgents] = useState(null);
  const [error, setError] = useState(false);

  useEffect(() => {
    getAgents().then(setAgents).catch(() => setError(true));
  }, []);

  if (error) return <div className="error">Failed to load fleet status.</div>;
  if (!agents) return <div className="loading">Loading…</div>;
  if (agents.length === 0) return <div className="empty">No agents registered yet.</div>;

  const byStatus = { online: 0, offline: 0, outdated: 0 };
  for (const a of agents) {
    if (byStatus[a.status] !== undefined) byStatus[a.status]++;
  }
  // Only the agents that actually need attention — the full roster already
  // lives on the Agents page.
  const needsAttention = agents
    .filter(a => a.status === 'offline' || a.status === 'outdated')
    .slice(0, 6);

  return (
    <div>
      <div style={{ display: 'flex', gap: '0.6rem', marginBottom: needsAttention.length ? '1rem' : 0, flexWrap: 'wrap' }}>
        <span className="badge online">{byStatus.online} online</span>
        <span className="badge outdated">{byStatus.outdated} outdated</span>
        <span className="badge offline">{byStatus.offline} offline</span>
      </div>
      {needsAttention.length > 0 && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
          {needsAttention.map(a => (
            <div key={a.id} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: 13 }}>
              <span>{a.hostname}</span>
              <span className={`badge ${a.status}`}>{a.status}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// Stats refresh on a timer since this page is often left open on a wall
// display or a browser tab — without it, "Online Agents" etc. silently go
// stale with no visual cue, unlike the charts below which refetch on every
// range change.
const STATS_REFRESH_MS = 60_000;

export default function Dashboard() {
  const [summary, setSummary] = useState(null);
  const [summaryError, setSummaryError] = useState(null);
  const [activeUsers, setActiveUsers] = useState(null);
  const [activeUsersError, setActiveUsersError] = useState(false);
  const [labs, setLabs] = useState([]);
  const globalFilters = useGlobalFilters();
  const { range, isCustomReady, effectiveRange, filters } = globalFilters;

  useEffect(() => {
    getLabs().then(setLabs).catch(() => {});
  }, []);

  useEffect(() => {
    const load = () => {
      getSummary().then(res => { setSummary(res); setSummaryError(null); }).catch(e => setSummaryError(e.message));
      getActiveUsers()
        .then(res => { setActiveUsers(res?.data?.result?.length ?? 0); setActiveUsersError(false); })
        .catch(() => setActiveUsersError(true));
    };
    load();
    const id = setInterval(load, STATS_REFRESH_MS);
    return () => clearInterval(id);
  }, []);

  return (
    <div>
      <h2>Dashboard</h2>

      <GlobalFilterBar filters={globalFilters} labs={labs} showMachine={false} />

      {range === 'custom' && !isCustomReady && (
        <div className="warning-banner">
          Select a valid start and end time to load data.
        </div>
      )}

      {summaryError && <div className="error" style={{ marginTop: '1rem' }}>{summaryError}</div>}
      {!summary && !summaryError && <div className="loading" style={{ padding: '1rem' }}>Loading…</div>}
      {summary && (
        <div className="stats-grid" style={{ marginTop: '1rem' }}>
          <div className="stat-card">
            <span className="stat-value">{summary.totalAgents}</span>
            <span className="stat-label">Total Agents</span>
          </div>
          <div className="stat-card">
            <span className="stat-value" style={{ color: 'var(--success)' }}>{summary.onlineAgents}</span>
            <span className="stat-label">Online</span>
          </div>
          <div className="stat-card">
            <span className="stat-value">{summary.totalLabs}</span>
            <span className="stat-label">Labs</span>
          </div>
          <div className="stat-card">
            <span className="stat-value">{summary.totalMappings}</span>
            <span className="stat-label">Mappings</span>
          </div>
          <div className="stat-card">
            <span className="stat-value" style={{ color: activeUsersError ? 'var(--danger)' : 'var(--accent)' }}>
              {activeUsersError ? '—' : activeUsers === null ? '…' : activeUsers}
            </span>
            <span className="stat-label">
              Active Users{activeUsersError && <span title="Failed to load active users">⚠</span>}
            </span>
          </div>
        </div>
      )}

      {(range !== 'custom' || isCustomReady) && (
        <>
          <div className="panel-grid">
            <div className="chart-card">
              <h3 style={{ marginTop: 0, marginBottom: '1rem' }}>Top Applications by Launch Count</h3>
              <TopAppsChart range={effectiveRange} filters={filters} />
            </div>
            <div className="chart-card">
              <h3 style={{ marginTop: 0, marginBottom: '1rem' }}>Usage by Lab</h3>
              <LabUsageChart range={effectiveRange} filters={filters} />
            </div>
          </div>

          <div className="panel-grid">
            <div className="chart-card">
              <h3 style={{ marginTop: 0, marginBottom: '1rem' }}>Fleet Health</h3>
              <FleetHealthPanel />
            </div>
            <div className="chart-card">
              <h3 style={{ marginTop: 0, marginBottom: '1rem' }}>Recent Privilege Elevations</h3>
              <RecentElevationsPanel range={effectiveRange} filters={filters} />
            </div>
          </div>
        </>
      )}
    </div>
  );
}
