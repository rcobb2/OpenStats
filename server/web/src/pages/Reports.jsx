import { useState, useEffect, useMemo } from 'react';
import {
  BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer, Cell,
} from 'recharts';
import {
  getTopAppsByLaunches,
  getTopAppsByUsage,
  getTopAppsByForeground,
  getBottomAppsByLaunches,
  getUsageByLab,
  parsePromVector,
  exportTopAppsByLaunches,
  exportTopAppsByForeground,
  exportBottomAppsByLaunches,
  exportBottomAppsByForeground,
  getAgents,
  getLabs,
  getTopDevicesBySessions,
  getTopUsersByLogins,
  getTopUsersBySessionTime,
  getAvgSessionTime,
  ignoreApp,
} from '../api';

const CHART_COLORS = [
  '#4f8ff7','#43b581','#f0a030','#e55353','#a78bfa',
  '#34d399','#fb923c','#60a5fa','#f472b6','#818cf8',
];

// Format a datetime-local string defaulting to now minus offsetHours
function defaultDatetime(offsetHours = 0) {
  const d = new Date(Date.now() - offsetHours * 3600 * 1000);
  return d.toISOString().slice(0, 16);
}

function HBarChart({ data, valueLabel = 'value', roundValues = false, height = 300, onIgnore }) {
  if (data === null) return <div className="loading" style={{ padding: '1rem' }}>Loading…</div>;
  if (data === false) return <div style={{ padding: '1rem', color: 'var(--error, #e55353)' }}>Failed to load data.</div>;
  if (data.length === 0) return <div style={{ padding: '1rem', color: 'var(--text-dim)' }}>No data for this period.</div>;

  const CustomTooltip = ({ active, payload }) => {
    if (!active || !payload?.[0]) return null;
    const { name, value, category } = payload[0].payload;
    const display = typeof value === 'number'
      ? value.toLocaleString(undefined, { maximumFractionDigits: roundValues ? 0 : 1 })
      : value;
    return (
      <div style={{ background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 6, padding: '0.5rem 0.75rem', fontSize: 13, maxWidth: 220 }}>
        <div style={{ color: 'var(--text)', fontWeight: 500, marginBottom: 2 }}>{name}</div>
        <div style={{ color: 'var(--text-dim)' }}>{category || valueLabel}: {display}</div>
        {onIgnore && (
          <button
            onClick={e => { e.stopPropagation(); onIgnore(name); }}
            style={{ marginTop: '0.4rem', fontSize: 11, color: 'var(--error, #e55353)', background: 'none', border: '1px solid var(--error, #e55353)', borderRadius: 3, cursor: 'pointer', padding: '1px 6px' }}
          >
            Ignore this app
          </button>
        )}
      </div>
    );
  };

  return (
    <ResponsiveContainer width="100%" height={height}>
      <BarChart layout="vertical" data={data} margin={{ top: 4, right: 24, bottom: 4, left: 8 }}>
        <XAxis
          type="number"
          tick={{ fill: 'var(--text-dim)', fontSize: 11 }}
          axisLine={{ stroke: 'var(--border)' }}
          tickLine={false}
          allowDecimals={!roundValues}
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
        <Tooltip content={<CustomTooltip />} cursor={{ fill: 'rgba(255,255,255,0.04)' }} />
        <Bar dataKey="value" radius={[0, 4, 4, 0]} maxBarSize={22} fill={CHART_COLORS[0]}>
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

function applyAppFilter(data, appFilter) {
  if (!appFilter || !Array.isArray(data)) return data;
  const q = appFilter.toLowerCase();
  return data.filter(r => r.name.toLowerCase().includes(q));
}

function UserBehaviorReport({ range, filters, appFilter, onIgnore }) {
  const [foreground, setForeground] = useState(null);
  const [launches, setLaunches] = useState(null);
  const [topDevices, setTopDevices] = useState(null);
  const [topUserLogins, setTopUserLogins] = useState(null);
  const [userSessionTime, setUserSessionTime] = useState(null);
  const [avgSession, setAvgSession] = useState(null);

  useEffect(() => {
    setForeground(null);
    setLaunches(null);
    setTopDevices(null);
    setTopUserLogins(null);
    setUserSessionTime(null);
    setAvgSession(null);

    getTopAppsByUsage(range, 10, filters)
      .then(r => setForeground(parsePromVector(r))).catch(() => setForeground(false));
    getTopAppsByLaunches(range, 10, filters)
      .then(r => setLaunches(parsePromVector(r))).catch(() => setLaunches(false));
    getTopDevicesBySessions(range, 10, filters)
      .then(r => setTopDevices(parsePromVector(r, 'hostname'))).catch(() => setTopDevices(false));
    getTopUsersByLogins(range, 10, filters)
      .then(r => setTopUserLogins(parsePromVector(r, 'user'))).catch(() => setTopUserLogins(false));
    getTopUsersBySessionTime(range, 10, filters)
      .then(r => setUserSessionTime(parsePromVector(r, 'user'))).catch(() => setUserSessionTime(false));
    getAvgSessionTime(range, 10, filters)
      .then(r => setAvgSession(parsePromVector(r, 'user'))).catch(() => setAvgSession(false));
  }, [range, filters]);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(420px, 1fr))', gap: '1.25rem' }}>
        <ChartCard title="Top 10 Apps by Active Time">
          <HBarChart data={applyAppFilter(foreground, appFilter)} valueLabel="hours" height={300} onIgnore={onIgnore} />
        </ChartCard>
        <ChartCard title="Top 10 Apps by Launch Count">
          <HBarChart data={applyAppFilter(launches, appFilter)} valueLabel="launches" roundValues height={300} onIgnore={onIgnore} />
        </ChartCard>
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(420px, 1fr))', gap: '1.25rem' }}>
        <ChartCard title="Top 10 Most Signed-In Devices">
          <HBarChart data={topDevices} valueLabel="logins" roundValues height={300} />
        </ChartCard>
        <ChartCard title="Top 10 Users by Login Count">
          <HBarChart data={topUserLogins} valueLabel="logins" roundValues height={300} />
        </ChartCard>
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(420px, 1fr))', gap: '1.25rem' }}>
        <ChartCard title="Top 10 Users by Total Session Time">
          <HBarChart data={userSessionTime} valueLabel="hours" height={300} />
        </ChartCard>
        <ChartCard title="Average Session Duration per User">
          <HBarChart data={avgSession} valueLabel="minutes" height={300} />
        </ChartCard>
      </div>
    </div>
  );
}

function LabUsageReport({ range, filters, appFilter }) {
  const [data, setData] = useState(null);

  useEffect(() => {
    setData(null);
    getUsageByLab(range, filters).then(res => {
      const raw = parsePromVector(res);
      const byLab = {};
      raw.forEach(r => {
        const lab = r.lab || 'Unassigned';
        byLab[lab] = (byLab[lab] || 0) + r.value;
      });
      const labData = Object.entries(byLab)
        .map(([lab, val]) => ({ name: lab, value: Math.round(val / 3600 * 10) / 10 }))
        .sort((a, b) => b.value - a.value);
      setData(labData);
    }).catch(() => setData(false));
  }, [range, filters]);

  return (
    <ChartCard title="Usage Hours by Lab">
      <HBarChart
        data={applyAppFilter(data, appFilter)}
        valueLabel="hours"
        height={Math.max(240, (data?.length ?? 5) * 36)}
      />
    </ChartCard>
  );
}

function SoftwareMeteringReport({ range, filters, appFilter, exporting, handleExport, onIgnore }) {
  const [topLaunches, setTopLaunches] = useState(null);
  const [bottomLaunches, setBottomLaunches] = useState(null);
  const [topForeground, setTopForeground] = useState(null);

  useEffect(() => {
    setTopLaunches(null);
    setBottomLaunches(null);
    setTopForeground(null);
    getTopAppsByLaunches(range, 10, filters).then(r => setTopLaunches(parsePromVector(r))).catch(() => setTopLaunches(false));
    getBottomAppsByLaunches(range, 10, filters).then(r => setBottomLaunches(parsePromVector(r))).catch(() => setBottomLaunches(false));
    getTopAppsByUsage(range, 10, filters).then(r => setTopForeground(parsePromVector(r))).catch(() => setTopForeground(false));
  }, [range, filters]);

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
          <HBarChart data={applyAppFilter(topLaunches, appFilter)} valueLabel="launches" roundValues height={300} onIgnore={onIgnore} />
        </ChartCard>
        <ChartCard title="Top 10 Apps — Active Time">
          <HBarChart data={applyAppFilter(topForeground, appFilter)} valueLabel="hours" height={300} onIgnore={onIgnore} />
        </ChartCard>
      </div>
      <ChartCard title="Bottom 10 Apps — Launch Count (Underutilized)">
        <HBarChart data={applyAppFilter(bottomLaunches, appFilter)} valueLabel="launches" roundValues height={300} onIgnore={onIgnore} />
      </ChartCard>
    </div>
  );
}

const labelStyle = { color: 'var(--text-dim)', fontSize: '0.85rem', marginRight: '0.3rem' };
const ctrlStyle = { display: 'flex', alignItems: 'center', gap: '0.3rem' };

export default function Reports() {
  const [range, setRange] = useState('24h');
  const [customStart, setCustomStart] = useState(() => defaultDatetime(24));
  const [customEnd, setCustomEnd] = useState(() => defaultDatetime(0));
  const [hostname, setHostname] = useState('');
  const [lab, setLab] = useState('');
  const [appFilter, setAppFilter] = useState('');
  const [reportType, setReportType] = useState('user');
  const [exporting, setExporting] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);
  const [agents, setAgents] = useState([]);
  const [labs, setLabs] = useState([]);

  useEffect(() => {
    getAgents().then(setAgents).catch(() => {});
    getLabs().then(setLabs).catch(() => {});
  }, []);

  const isCustomReady = range === 'custom' && customStart && customEnd
    && new Date(customEnd) > new Date(customStart);

  // Memoize so the object identity only changes when filter values actually change,
  // preventing sub-component useEffects from re-firing on every parent render.
  const filters = useMemo(() => ({
    ...(isCustomReady ? { start: customStart, end: customEnd } : {}),
    ...(hostname ? { hostname } : {}),
    ...(lab ? { lab } : {}),
  }), [isCustomReady, customStart, customEnd, hostname, lab]);

  // Encode all active filter params into the key so chart components reload on any filter change.
  const effectiveRange = isCustomReady ? `${customStart}~${customEnd}` : range;
  const chartKey = `${refreshKey}-${effectiveRange}-${hostname}-${lab}`;

  const handleIgnore = async (name) => {
    if (!window.confirm(`Hide "${name}" from all charts?\nYou can re-enable it in the Mappings page.`)) return;
    try {
      await ignoreApp(name);
      setRefreshKey(k => k + 1);
    } catch (err) {
      alert('Failed to ignore app: ' + err.message);
    }
  };

  const handleExport = async (exportFn) => {
    setExporting(true);
    try {
      await exportFn(range === 'custom' ? '7d' : range, filters);
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
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: '0.75rem', marginBottom: '1rem' }}>
        <h2 style={{ margin: 0 }}>{titles[reportType]}</h2>
        <button
          className="btn-secondary"
          onClick={() => setRefreshKey(k => k + 1)}
          title="Reload chart data from server"
          style={{ padding: '0.3rem 0.8rem' }}
        >
          ↺ Refresh
        </button>
      </div>

      <div style={{ display: 'flex', gap: '0.75rem', flexWrap: 'wrap', alignItems: 'center', marginBottom: '1.25rem', padding: '0.75rem 1rem', background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 8 }}>
        <div style={ctrlStyle}>
          <label style={labelStyle}>Report</label>
          <select value={reportType} onChange={e => setReportType(e.target.value)}>
            <option value="user">User Behavior</option>
            <option value="hardware">Hardware Utilization</option>
            <option value="software">Software Metering</option>
          </select>
        </div>

        <div style={ctrlStyle}>
          <label style={labelStyle}>Time Range</label>
          <select value={range} onChange={e => setRange(e.target.value)}>
            <option value="1h">Last Hour</option>
            <option value="24h">Last 24 Hours</option>
            <option value="7d">Last 7 Days</option>
            <option value="30d">Last 30 Days</option>
            <option value="custom">Custom…</option>
          </select>
        </div>

        {range === 'custom' && (
          <>
            <div style={ctrlStyle}>
              <label style={labelStyle}>From</label>
              <input
                type="datetime-local"
                value={customStart}
                onChange={e => setCustomStart(e.target.value)}
                style={{ fontSize: '0.85rem' }}
              />
            </div>
            <div style={ctrlStyle}>
              <label style={labelStyle}>To</label>
              <input
                type="datetime-local"
                value={customEnd}
                onChange={e => setCustomEnd(e.target.value)}
                style={{ fontSize: '0.85rem' }}
              />
            </div>
          </>
        )}

        <div style={ctrlStyle}>
          <label style={labelStyle}>Machine</label>
          <select value={hostname} onChange={e => { setHostname(e.target.value); if (e.target.value) setLab(''); }}>
            <option value="">All Machines</option>
            {agents.map(a => (
              <option key={a.id} value={a.hostname}>{a.hostname}</option>
            ))}
          </select>
        </div>

        <div style={ctrlStyle}>
          <label style={labelStyle}>Lab</label>
          <select value={lab} onChange={e => { setLab(e.target.value); if (e.target.value) setHostname(''); }}>
            <option value="">All Labs</option>
            {labs.map(l => (
              <option key={l.id} value={l.name}>{l.name}</option>
            ))}
          </select>
        </div>

        <div style={ctrlStyle}>
          <label style={labelStyle}>App</label>
          <input
            type="text"
            placeholder="Filter…"
            value={appFilter}
            onChange={e => setAppFilter(e.target.value)}
            style={{ width: '120px', fontSize: '0.85rem' }}
          />
        </div>
      </div>

      {range === 'custom' && !isCustomReady && (
        <div style={{ padding: '0.75rem 1rem', marginBottom: '1rem', background: 'rgba(240,160,48,0.12)', border: '1px solid var(--border)', borderRadius: 6, color: 'var(--text-dim)', fontSize: '0.9rem' }}>
          Select a valid start and end time to load data.
        </div>
      )}

      {(range !== 'custom' || isCustomReady) && (
        <>
          {reportType === 'user' && (
            <UserBehaviorReport key={chartKey} range={effectiveRange} filters={filters} appFilter={appFilter} onIgnore={handleIgnore} />
          )}
          {reportType === 'hardware' && (
            <LabUsageReport key={chartKey} range={effectiveRange} filters={filters} appFilter={appFilter} />
          )}
          {reportType === 'software' && (
            <SoftwareMeteringReport
              key={chartKey}
              range={effectiveRange}
              filters={filters}
              appFilter={appFilter}
              exporting={exporting}
              handleExport={handleExport}
              onIgnore={handleIgnore}
            />
          )}
        </>
      )}
    </div>
  );
}
