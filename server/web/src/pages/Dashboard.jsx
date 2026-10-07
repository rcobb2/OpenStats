import { useState, useEffect } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import {
  BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer, Cell,
} from 'recharts';
import {
  getSummary, getTopAppsByLaunches, getActiveUsers, getUsageByLab,
  getTopAppsByElevations, getAgents, getLabs, parsePromVector,
} from '../api';
import { useGlobalFilters } from '../hooks/useGlobalFilters';
import GlobalFilterBar from '../components/GlobalFilterBar';
import MiniBarList from '../components/MiniBarList';

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
function LabUsageChart({ range, filters, labs }) {
  const [data, setData] = useState(null);
  const [error, setError] = useState(null);
  const navigate = useNavigate();
  const labIdByName = Object.fromEntries(labs.map(l => [l.name, l.id]));

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
        <Bar
          dataKey="value"
          radius={[0, 4, 4, 0]}
          maxBarSize={22}
          onClick={(d) => {
            const name = d?.payload?.name ?? d?.name;
            const labId = labIdByName[name];
            if (labId) navigate(`/labs/${labId}`);
          }}
        >
          {data.map((d, i) => (
            <Cell
              key={i}
              fill={CHART_COLORS[i % CHART_COLORS.length]}
              cursor={labIdByName[d.name] ? 'pointer' : 'default'}
            />
          ))}
        </Bar>
      </BarChart>
    </ResponsiveContainer>
  );
}

function RecentElevationsPanel({ range, filters }) {
  const [data, setData] = useState(null);
  const [error, setError] = useState(false);

  useEffect(() => {
    setData(null);
    setError(false);
    getTopAppsByElevations(range, 6, filters)
      .then(res => setData(parsePromVector(res)))
      .catch(() => setError(true));
  }, [range, filters]);

  return (
    <MiniBarList
      data={data}
      error={error}
      colors={CHART_COLORS}
      emptyMessage="No privilege elevations in this period."
    />
  );
}

// Triage-first, not "one panel among several": a fleet with agents needing
// attention should be unmissable at the top of the page, not competing for
// space in a chart grid below the fold. Quiet (a single line) when nothing
// needs attention, prominent when something does — the same shape Tenable
// and Datadog use for "what's Critical right now" vs. a clean bill of
// health. Deliberately fleet-wide regardless of the page's lab scope
// selector — narrowing it to one lab would hide an offline machine in a lab
// you forgot to filter out.
function TriageBanner() {
  const [agents, setAgents] = useState(null);
  const [error, setError] = useState(false);

  useEffect(() => {
    getAgents().then(setAgents).catch(() => setError(true));
  }, []);

  if (error) return <div className="error">Failed to load fleet status.</div>;
  if (!agents) return <div className="loading">Loading fleet status…</div>;
  if (agents.length === 0) return <div className="empty">No agents registered yet.</div>;

  const byStatus = { online: 0, offline: 0, outdated: 0 };
  for (const a of agents) {
    if (byStatus[a.status] !== undefined) byStatus[a.status]++;
  }
  const needsAttention = agents
    .filter(a => a.status === 'offline' || a.status === 'outdated')
    // Offline first — a dead machine is more urgent than one that's just
    // behind on updates.
    .sort((a, b) => (a.status === 'offline' ? 0 : 1) - (b.status === 'offline' ? 0 : 1));

  if (needsAttention.length === 0) {
    return (
      <div className="triage-banner ok">
        <span className="triage-icon">✓</span>
        <span>All {byStatus.online} agent{byStatus.online !== 1 ? 's' : ''} online and up to date.</span>
      </div>
    );
  }

  return (
    <div className="triage-banner alert">
      <div className="triage-header">
        <span className="triage-icon">⚠</span>
        <span>{needsAttention.length} agent{needsAttention.length !== 1 ? 's' : ''} need attention</span>
        <span className="triage-counts">
          {byStatus.offline > 0 && <span className="badge offline">{byStatus.offline} offline</span>}
          {byStatus.outdated > 0 && <span className="badge outdated">{byStatus.outdated} outdated</span>}
          <span className="badge online">{byStatus.online} online</span>
        </span>
        <Link to="/agents" className="btn-secondary" style={{ marginLeft: 'auto', fontSize: 12, padding: '3px 10px' }}>
          View all →
        </Link>
      </div>
      <div className="triage-list">
        {needsAttention.slice(0, 8).map(a => (
          <Link key={a.id} to={`/agents/${a.id}`} className="triage-item">
            {a.hostname}
            <span className={`badge ${a.status}`}>{a.status}</span>
          </Link>
        ))}
        {needsAttention.length > 8 && (
          <Link to="/agents" className="triage-item">+{needsAttention.length - 8} more</Link>
        )}
      </div>
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

      <TriageBanner />

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
          <Link to="/agents" className="stat-card">
            <span className="stat-value">{summary.totalAgents}</span>
            <span className="stat-label">Total Agents</span>
          </Link>
          <Link to="/agents" className="stat-card">
            <span className="stat-value" style={{ color: 'var(--success)' }}>{summary.onlineAgents}</span>
            <span className="stat-label">Online</span>
          </Link>
          <Link to="/labs" className="stat-card">
            <span className="stat-value">{summary.totalLabs}</span>
            <span className="stat-label">Labs</span>
          </Link>
          <Link to="/mappings" className="stat-card">
            <span className="stat-value">{summary.totalMappings}</span>
            <span className="stat-label">Mappings</span>
          </Link>
          <Link to="/users" className="stat-card">
            <span className="stat-value" style={{ color: activeUsersError ? 'var(--danger)' : 'var(--accent)' }}>
              {activeUsersError ? '—' : activeUsers === null ? '…' : activeUsers}
            </span>
            <span className="stat-label">
              Active Users{activeUsersError && <span title="Failed to load active users">⚠</span>}
            </span>
          </Link>
        </div>
      )}

      {(range !== 'custom' || isCustomReady) && (
        <div className="panel-grid">
          <div className="chart-card">
            <h3 style={{ marginTop: 0, marginBottom: '1rem' }}>Top Applications by Launch Count</h3>
            <TopAppsChart range={effectiveRange} filters={filters} />
          </div>
          <div className="chart-card">
            <h3 style={{ marginTop: 0, marginBottom: '1rem' }}>Usage by Lab</h3>
            <LabUsageChart range={effectiveRange} filters={filters} labs={labs} />
          </div>
          <div className="chart-card">
            <h3 style={{ marginTop: 0, marginBottom: '1rem' }}>Recent Privilege Elevations</h3>
            <RecentElevationsPanel range={effectiveRange} filters={filters} />
          </div>
        </div>
      )}
    </div>
  );
}
