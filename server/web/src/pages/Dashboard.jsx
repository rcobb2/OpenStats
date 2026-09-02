import { useState, useEffect } from 'react';
import {
  BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer, Cell,
} from 'recharts';
import { getSummary, getTopAppsByLaunches, getActiveUsers, parsePromVector } from '../api';

const CHART_COLORS = [
  '#4f8ff7','#43b581','#f0a030','#e55353','#a78bfa',
  '#34d399','#fb923c','#60a5fa','#f472b6','#818cf8',
];

function TopAppsChart({ range }) {
  const [data, setData] = useState(null);
  const [error, setError] = useState(null);

  useEffect(() => {
    setData(null);
    setError(null);
    getTopAppsByLaunches(range, 10)
      .then(res => setData(parsePromVector(res)))
      .catch(e => setError(e.message));
  }, [range]);

  if (error) return <div className="error" style={{ padding: '1rem' }}>Chart unavailable: {error}</div>;
  if (!data) return <div className="loading" style={{ padding: '1rem' }}>Loading chart…</div>;
  if (data.length === 0) return <div style={{ padding: '1rem', color: 'var(--text-dim)' }}>No data for this period.</div>;

  return (
    <ResponsiveContainer width="100%" height={320}>
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

export default function Dashboard() {
  const [summary, setSummary] = useState(null);
  const [summaryError, setSummaryError] = useState(null);
  const [activeUsers, setActiveUsers] = useState(null);
  const [range, setRange] = useState('24h');

  useEffect(() => {
    getSummary().then(setSummary).catch(e => setSummaryError(e.message));
    getActiveUsers()
      .then(res => setActiveUsers(res?.data?.result?.length ?? 0))
      .catch(() => setActiveUsers('—'));
  }, []);

  return (
    <div>
      <h2>Dashboard</h2>

      {summaryError && <div className="error">{summaryError}</div>}
      {summary && (
        <div className="stats-grid">
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
            <span className="stat-value" style={{ color: 'var(--accent)' }}>
              {activeUsers === null ? '…' : activeUsers}
            </span>
            <span className="stat-label">Active Users</span>
          </div>
        </div>
      )}

      <div style={{ background: 'var(--surface)', borderRadius: 8, border: '1px solid var(--border)', padding: '1.25rem', marginTop: '2rem' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '1rem' }}>
          <h3 style={{ margin: 0 }}>Top Applications by Launch Count</h3>
          <select value={range} onChange={e => setRange(e.target.value)}>
            <option value="1h">Last Hour</option>
            <option value="24h">Last 24 Hours</option>
            <option value="7d">Last 7 Days</option>
            <option value="30d">Last 30 Days</option>
          </select>
        </div>
        <TopAppsChart range={range} />
      </div>
    </div>
  );
}
