import { useState, useEffect } from 'react';
import {
  BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer, Cell, Legend,
} from 'recharts';
import {
  getTopAppsByLaunches,
  getTopAppsByForeground,
  getBottomAppsByLaunches,
  getUsageByLab,
  parsePromVector,
  exportTopAppsByLaunches,
  exportTopAppsByForeground,
  exportBottomAppsByLaunches,
  exportBottomAppsByForeground,
} from '../api';

const CHART_COLORS = [
  '#4f8ff7','#43b581','#f0a030','#e55353','#a78bfa',
  '#34d399','#fb923c','#60a5fa','#f472b6','#818cf8',
];

function HBarChart({ data, valueLabel = 'value', color = '#4f8ff7', height = 300 }) {
  if (data === null) return <div className="loading" style={{ padding: '1rem' }}>Loading…</div>;
  if (data === false) return <div style={{ padding: '1rem', color: 'var(--error, #e55353)' }}>Failed to load data.</div>;
  if (data.length === 0) return <div style={{ padding: '1rem', color: 'var(--text-dim)' }}>No data for this period.</div>;

  return (
    <ResponsiveContainer width="100%" height={height}>
      <BarChart layout="vertical" data={data} margin={{ top: 4, right: 24, bottom: 4, left: 8 }}>
        <XAxis
          type="number"
          tick={{ fill: 'var(--text-dim)', fontSize: 11 }}
          axisLine={{ stroke: 'var(--border)' }}
          tickLine={false}
          label={{ value: valueLabel, position: 'insideBottomRight', offset: -4, fill: 'var(--text-dim)', fontSize: 11 }}
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
            typeof v === 'number' ? v.toLocaleString(undefined, { maximumFractionDigits: 1 }) : v,
            payload.category || valueLabel,
          ]}
        />
        <Bar dataKey="value" radius={[0, 4, 4, 0]} maxBarSize={22} fill={color}>
          {data.map((_, i) => (
            <Cell key={i} fill={CHART_COLORS[i % CHART_COLORS.length]} />
          ))}
        </Bar>
      </BarChart>
    </ResponsiveContainer>
  );
}

function ChartCard({ title, children }) {
  return (
    <div style={{ background: 'var(--surface)', borderRadius: 8, border: '1px solid var(--border)', padding: '1.25rem' }}>
      <h3 style={{ margin: '0 0 1rem' }}>{title}</h3>
      {children}
    </div>
  );
}

function UserBehaviorReport({ range }) {
  const [foreground, setForeground] = useState(null);
  const [launches, setLaunches] = useState(null);

  useEffect(() => {
    setForeground(null);
    setLaunches(null);
    getTopAppsByForeground(range, 10).then(r => setForeground(parsePromVector(r))).catch(() => setForeground(false));
    getTopAppsByLaunches(range, 10).then(r => setLaunches(parsePromVector(r))).catch(() => setLaunches(false));
  }, [range]);

  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(420px, 1fr))', gap: '1.25rem' }}>
      <ChartCard title="Top 10 Apps by Active Time">
        <HBarChart data={foreground} valueLabel="hours" height={300} />
      </ChartCard>
      <ChartCard title="Top 10 Apps by Launch Count">
        <HBarChart data={launches} valueLabel="launches" height={300} />
      </ChartCard>
    </div>
  );
}

function LabUsageReport({ range }) {
  const [data, setData] = useState(null);

  useEffect(() => {
    setData(null);
    getUsageByLab(range).then(res => {
      const raw = parsePromVector(res);
      // Sum usage seconds by lab across all apps
      const byLab = {};
      raw.forEach(r => {
        const lab = r.lab || 'unknown';
        byLab[lab] = (byLab[lab] || 0) + r.value;
      });
      const labData = Object.entries(byLab)
        .map(([lab, val]) => ({ name: lab, value: Math.round(val / 3600 * 10) / 10 }))
        .sort((a, b) => b.value - a.value);
      setData(labData);
    }).catch(() => setData(false));
  }, [range]);

  return (
    <ChartCard title="Usage Hours by Lab">
      <HBarChart data={data} valueLabel="hours" height={Math.max(240, (data?.length ?? 5) * 36)} />
    </ChartCard>
  );
}

function SoftwareMeteringReport({ range, exporting, handleExport }) {
  const [topLaunches, setTopLaunches] = useState(null);
  const [bottomLaunches, setBottomLaunches] = useState(null);
  const [topForeground, setTopForeground] = useState(null);

  useEffect(() => {
    setTopLaunches(null);
    setBottomLaunches(null);
    setTopForeground(null);
    getTopAppsByLaunches(range, 10).then(r => setTopLaunches(parsePromVector(r))).catch(() => setTopLaunches(false));
    getBottomAppsByLaunches(range, 10).then(r => setBottomLaunches(parsePromVector(r))).catch(() => setBottomLaunches(false));
    getTopAppsByForeground(range, 10).then(r => setTopForeground(parsePromVector(r))).catch(() => setTopForeground(false));
  }, [range]);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}>
      <div style={{ display: 'flex', gap: '0.5rem', flexWrap: 'wrap' }}>
        <button className="btn-secondary" onClick={() => handleExport(exportTopAppsByLaunches)} disabled={exporting}>
          CSV: Top 10 Launches
        </button>
        <button className="btn-secondary" onClick={() => handleExport(exportTopAppsByForeground)} disabled={exporting}>
          CSV: Top 10 Active Time
        </button>
        <button className="btn-secondary" onClick={() => handleExport(exportBottomAppsByLaunches)} disabled={exporting}>
          CSV: Bottom 10 Launches
        </button>
        <button className="btn-secondary" onClick={() => handleExport(exportBottomAppsByForeground)} disabled={exporting}>
          CSV: Bottom 10 Active Time
        </button>
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(420px, 1fr))', gap: '1.25rem' }}>
        <ChartCard title="Top 10 Apps — Launch Count">
          <HBarChart data={topLaunches} valueLabel="launches" height={300} />
        </ChartCard>
        <ChartCard title="Top 10 Apps — Active Time">
          <HBarChart data={topForeground} valueLabel="hours" height={300} />
        </ChartCard>
      </div>
      <ChartCard title="Bottom 10 Apps — Launch Count (Underutilized)">
        <HBarChart data={bottomLaunches} valueLabel="launches" height={300} />
      </ChartCard>
    </div>
  );
}

export default function Reports() {
  const [range, setRange] = useState('24h');
  const [reportType, setReportType] = useState('user');
  const [exporting, setExporting] = useState(false);

  const handleExport = async (exportFn) => {
    setExporting(true);
    try {
      await exportFn(range);
    } catch (err) {
      alert('Export failed: ' + err.message);
    } finally {
      setExporting(false);
    }
  };

  const titles = {
    user: 'User Behavior Analytics',
    hardware: 'Hardware & Lab Utilization',
    software: 'Software Metering',
  };

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: '1rem', marginBottom: '1.5rem' }}>
        <h2 style={{ margin: 0 }}>{titles[reportType]}</h2>
        <div style={{ display: 'flex', gap: '1rem', flexWrap: 'wrap', alignItems: 'center' }}>
          <div>
            <label>Report: </label>
            <select value={reportType} onChange={e => setReportType(e.target.value)}>
              <option value="user">User Behavior</option>
              <option value="hardware">Hardware Utilization</option>
              <option value="software">Software Metering</option>
            </select>
          </div>
          <div>
            <label>Time Range: </label>
            <select value={range} onChange={e => setRange(e.target.value)}>
              <option value="1h">Last Hour</option>
              <option value="24h">Last 24 Hours</option>
              <option value="7d">Last 7 Days</option>
              <option value="30d">Last 30 Days</option>
            </select>
          </div>
        </div>
      </div>

      {reportType === 'user' && <UserBehaviorReport range={range} />}
      {reportType === 'hardware' && <LabUsageReport range={range} />}
      {reportType === 'software' && (
        <SoftwareMeteringReport range={range} exporting={exporting} handleExport={handleExport} />
      )}
    </div>
  );
}
