import { useState, useEffect, useMemo } from 'react';
import {
  BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer, Cell,
  LineChart, Line, CartesianGrid, Legend,
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
  getTopAppsByElevations,
  getTopUsersByElevations,
  ignoreApp,
  getUtilizationOverTime,
} from '../api';

const CHART_COLORS = [
  '#4f8ff7','#43b581','#f0a030','#e55353','#a78bfa',
  '#34d399','#fb923c','#60a5fa','#f472b6','#818cf8',
];

// "View all" fetches every row in one shot rather than paging — matches the
// server's maxReportLimit ceiling (server/internal/api/reports.go), which is
// generously sized because even a large fleet's distinct apps/users fit in
// one in-memory sort.
const VIEW_ALL_LIMIT = 1000;

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

function ChartCard({ title, subtitle, children, onViewAll }) {
  return (
    <div style={{ background: 'var(--surface)', borderRadius: 8, border: '1px solid var(--border)', padding: '1.25rem' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: '1rem', gap: '0.75rem' }}>
        <div>
          <h3 style={{ margin: 0 }}>{title}</h3>
          {subtitle && <div style={{ fontSize: 12, color: 'var(--text-dim)', marginTop: 2 }}>{subtitle}</div>}
        </div>
        {onViewAll && (
          <button
            className="btn-secondary"
            style={{ fontSize: 12, padding: '3px 10px', whiteSpace: 'nowrap' }}
            onClick={onViewAll}
          >
            View all →
          </button>
        )}
      </div>
      {children}
    </div>
  );
}

// The global `th, td` rule sets a tight fixed padding plus text-overflow:
// ellipsis for every table in the app — fine for names, but it clips a
// 2-digit rank ("48") down to "4…" in this narrow column. Override both
// (inline styles win over the stylesheet's element selector) and drop the
// padding enough that even 3-digit ranks fit within VIEW_ALL_LIMIT.
const rankCellStyle = { width: 48, padding: '0.6rem 0.4rem', overflow: 'visible', textOverflow: 'unset' };

// Full-list companion to the top/bottom-N bar charts: those panels only ever
// request `limit` rows, so a user asking "is my app really only used by 10
// people?" has no way to see row 11 onward. This fetches once, with
// VIEW_ALL_LIMIT, when opened — no pagination, since that ceiling already
// covers realistic fleet sizes (see VIEW_ALL_LIMIT).
function ViewAllModal({ title, valueLabel, roundValues, fetcher, onClose }) {
  const [rows, setRows] = useState(null);

  useEffect(() => {
    let cancelled = false;
    setRows(null);
    fetcher()
      .then(data => { if (!cancelled) setRows(data); })
      .catch(() => { if (!cancelled) setRows(false); });
    return () => { cancelled = true; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const fmt = v => v.toLocaleString(undefined, { maximumFractionDigits: roundValues ? 0 : 1 });

  return (
    <div
      style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.55)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000 }}
      onClick={onClose}
    >
      <div
        style={{ background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 8, padding: '1.25rem', width: 'min(560px, 92vw)', maxHeight: '80vh', display: 'flex', flexDirection: 'column' }}
        onClick={e => e.stopPropagation()}
      >
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '0.75rem' }}>
          <h3 style={{ margin: 0 }}>{title}</h3>
          <button className="btn-secondary" style={{ fontSize: 12, padding: '3px 10px' }} onClick={onClose}>Close</button>
        </div>
        <div style={{ overflowY: 'auto', flex: 1 }}>
          {rows === null && <div className="loading" style={{ padding: '1rem' }}>Loading…</div>}
          {rows === false && <div style={{ padding: '1rem', color: 'var(--error, #e55353)' }}>Failed to load data.</div>}
          {Array.isArray(rows) && rows.length === 0 && (
            <div style={{ padding: '1rem', color: 'var(--text-dim)' }}>No data for this period.</div>
          )}
          {Array.isArray(rows) && rows.length > 0 && (
            <table>
              <thead>
                <tr>
                  <th style={rankCellStyle}>#</th>
                  <th>Name</th>
                  <th style={{ textAlign: 'right' }}>{valueLabel}</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((r, i) => (
                  <tr key={`${r.name}-${i}`}>
                    <td style={{ ...rankCellStyle, color: 'var(--text-dim)' }}>{i + 1}</td>
                    <td>{r.name}</td>
                    <td style={{ textAlign: 'right' }}>{fmt(r.value)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
        {Array.isArray(rows) && rows.length > 0 && (
          <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: '0.6rem' }}>
            {rows.length} row{rows.length === 1 ? '' : 's'}
            {rows.length >= VIEW_ALL_LIMIT ? ` (capped at ${VIEW_ALL_LIMIT})` : ''}
          </div>
        )}
      </div>
    </div>
  );
}

// Shared open/close state for a page's "view all" modal — one modal at a
// time is enough since only one can be open per click.
function useViewAllModal() {
  const [viewAll, setViewAll] = useState(null); // { title, valueLabel, roundValues, fetcher } | null
  const modal = viewAll && <ViewAllModal {...viewAll} onClose={() => setViewAll(null)} />;
  return [modal, setViewAll];
}

function applyAppFilter(data, appFilter) {
  if (!appFilter || !Array.isArray(data)) return data;
  const q = appFilter.toLowerCase();
  return data.filter(r => r.name.toLowerCase().includes(q));
}

// A filtered-down lab or a short window can genuinely have fewer than
// `limit` distinct entries — a single-lab filter is the common case, not a
// short window, so this doesn't blame time specifically. Checked against the
// *unfiltered* fetch (before the free-text appFilter search box narrows it
// further, which is expected and shouldn't trigger this note). `clause`
// should read naturally after "fewer than N" — e.g. "apps had usage",
// "devices had logins".
function sparseNote(data, limit, clause) {
  if (!Array.isArray(data) || data.length === 0 || data.length >= limit) return undefined;
  return `Showing all ${data.length} — fewer than ${limit} ${clause} for the current filters`;
}

// Login-derived metrics (device/user login counts, avg session duration) are
// documented as sparse under 30 days — lab machines are rarely signed out, so
// increase() over a short window frequently has nothing to show. Rather than
// let these three panels silently follow the page's short default (24h) and
// look broken with a single bar, floor their query window to 30 days
// regardless of the page-level selector. Explicit custom ranges are left
// alone — a user who picked a specific window asked for that window.
const LOGIN_METRIC_MIN_RANGE = '30d';
function floorLoginRange(range) {
  if (range === 'custom' || range.includes('~')) return range;
  const days = range.endsWith('d') ? parseInt(range, 10) : range.endsWith('h') ? parseInt(range, 10) / 24 : 0;
  return days >= 30 ? range : LOGIN_METRIC_MIN_RANGE;
}

function UserBehaviorReport({ range, filters, appFilter, onIgnore }) {
  const [foreground, setForeground] = useState(null);
  const [launches, setLaunches] = useState(null);
  const [topDevices, setTopDevices] = useState(null);
  const [topUserLogins, setTopUserLogins] = useState(null);
  const [userSessionTime, setUserSessionTime] = useState(null);
  const [avgSession, setAvgSession] = useState(null);
  const [elevatedApps, setElevatedApps] = useState(null);
  const [elevatingUsers, setElevatingUsers] = useState(null);

  const loginRange = floorLoginRange(range);
  const loginRangeFloored = loginRange !== range;

  useEffect(() => {
    setForeground(null);
    setLaunches(null);
    setTopDevices(null);
    setTopUserLogins(null);
    setUserSessionTime(null);
    setAvgSession(null);
    setElevatedApps(null);
    setElevatingUsers(null);

    getTopAppsByUsage(range, 10, filters)
      .then(r => setForeground(parsePromVector(r))).catch(() => setForeground(false));
    getTopAppsByLaunches(range, 10, filters)
      .then(r => setLaunches(parsePromVector(r))).catch(() => setLaunches(false));
    getTopDevicesBySessions(loginRange, 10, filters)
      .then(r => setTopDevices(parsePromVector(r, 'hostname'))).catch(() => setTopDevices(false));
    getTopUsersByLogins(loginRange, 10, filters)
      .then(r => setTopUserLogins(parsePromVector(r, 'user'))).catch(() => setTopUserLogins(false));
    getTopUsersBySessionTime(range, 10, filters)
      .then(r => setUserSessionTime(parsePromVector(r, 'user'))).catch(() => setUserSessionTime(false));
    getAvgSessionTime(loginRange, 10, filters)
      .then(r => setAvgSession(parsePromVector(r, 'user'))).catch(() => setAvgSession(false));
    getTopAppsByElevations(loginRange, 10, filters)
      .then(r => setElevatedApps(parsePromVector(r))).catch(() => setElevatedApps(false));
    getTopUsersByElevations(loginRange, 10, filters)
      .then(r => setElevatingUsers(parsePromVector(r, 'user'))).catch(() => setElevatingUsers(false));
  }, [range, loginRange, filters]);

  const loginSparse = loginRangeFloored
    ? 'Logins are sparse — showing last 30 days regardless of the range above'
    : undefined;
  // Users with under 3 logins in the window are omitted server-side — total
  // accrued time divided by 1-2 discrete sign-ins produces a distorted
  // "average" for shared/kiosk accounts that rarely sign fully out.
  const omittedNote = 'Users with under 3 logins in the window are omitted';

  const [viewAllModal, openViewAll] = useViewAllModal();

  const foregroundSubtitle = sparseNote(foreground, 10, 'apps had usage');
  const launchesSubtitle = sparseNote(launches, 10, 'apps had usage');
  const topDevicesSubtitle = [loginSparse, sparseNote(topDevices, 10, 'devices had logins')].filter(Boolean).join('. ');
  const topUserLoginsSubtitle = [loginSparse, sparseNote(topUserLogins, 10, 'users had logins')].filter(Boolean).join('. ');
  const userSessionTimeSubtitle = sparseNote(userSessionTime, 10, 'users had usage');
  // No sparseNote here: the 3-login floor is why this list is short, and a
  // "fewer than 10 had usage" note would flatly contradict the line right
  // above it — those users did have usage, they were filtered by login count.
  const avgSessionSubtitle = [loginSparse, omittedNote].filter(Boolean).join('. ');
  // Elevations only come from Windows agents and are rare events, so the panels
  // borrow the 30-day login floor rather than the headline range.
  const elevationSubtitle = [
    'Windows machines only',
    loginRangeFloored ? 'Elevations are sparse — showing last 30 days regardless of the range above' : undefined,
  ].filter(Boolean).join('. ');

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(420px, 1fr))', gap: '1.25rem' }}>
        <ChartCard
          title="Most Active Apps"
          subtitle={foregroundSubtitle}
          onViewAll={() => openViewAll({
            title: 'All Apps by Active Time', valueLabel: 'hours',
            fetcher: () => getTopAppsByUsage(range, VIEW_ALL_LIMIT, filters).then(r => applyAppFilter(parsePromVector(r), appFilter)),
          })}
        >
          <HBarChart data={applyAppFilter(foreground, appFilter)} valueLabel="hours" height={300} onIgnore={onIgnore} />
        </ChartCard>
        <ChartCard
          title="Most Launched Apps"
          subtitle={launchesSubtitle}
          onViewAll={() => openViewAll({
            title: 'All Apps by Launch Count', valueLabel: 'launches', roundValues: true,
            fetcher: () => getTopAppsByLaunches(range, VIEW_ALL_LIMIT, filters).then(r => applyAppFilter(parsePromVector(r), appFilter)),
          })}
        >
          <HBarChart data={applyAppFilter(launches, appFilter)} valueLabel="launches" roundValues height={300} onIgnore={onIgnore} />
        </ChartCard>
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(420px, 1fr))', gap: '1.25rem' }}>
        <ChartCard
          title="Most Signed-In Devices"
          subtitle={topDevicesSubtitle}
          onViewAll={() => openViewAll({
            title: 'All Devices by Login Count', valueLabel: 'logins', roundValues: true,
            fetcher: () => getTopDevicesBySessions(loginRange, VIEW_ALL_LIMIT, filters).then(r => parsePromVector(r, 'hostname')),
          })}
        >
          <HBarChart data={topDevices} valueLabel="logins" roundValues height={300} />
        </ChartCard>
        <ChartCard
          title="Most Frequent Logins"
          subtitle={topUserLoginsSubtitle}
          onViewAll={() => openViewAll({
            title: 'All Users by Login Count', valueLabel: 'logins', roundValues: true,
            fetcher: () => getTopUsersByLogins(loginRange, VIEW_ALL_LIMIT, filters).then(r => parsePromVector(r, 'user')),
          })}
        >
          <HBarChart data={topUserLogins} valueLabel="logins" roundValues height={300} />
        </ChartCard>
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(420px, 1fr))', gap: '1.25rem' }}>
        <ChartCard
          title="Most Active Users by Session Time"
          subtitle={userSessionTimeSubtitle}
          onViewAll={() => openViewAll({
            title: 'All Users by Total Session Time', valueLabel: 'hours',
            fetcher: () => getTopUsersBySessionTime(range, VIEW_ALL_LIMIT, filters).then(r => parsePromVector(r, 'user')),
          })}
        >
          <HBarChart data={userSessionTime} valueLabel="hours" height={300} />
        </ChartCard>
        <ChartCard
          title="Average Session Duration per User"
          subtitle={avgSessionSubtitle}
          onViewAll={() => openViewAll({
            title: 'All Users by Average Session Duration', valueLabel: 'minutes',
            fetcher: () => getAvgSessionTime(loginRange, VIEW_ALL_LIMIT, filters).then(r => parsePromVector(r, 'user')),
          })}
        >
          <HBarChart data={avgSession} valueLabel="minutes" height={300} />
        </ChartCard>
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(420px, 1fr))', gap: '1.25rem' }}>
        <ChartCard
          title="Top Elevated Apps"
          subtitle={elevationSubtitle}
          onViewAll={() => openViewAll({
            title: 'All Apps by UAC Elevation Count', valueLabel: 'elevations', roundValues: true,
            fetcher: () => getTopAppsByElevations(loginRange, VIEW_ALL_LIMIT, filters).then(r => parsePromVector(r)),
          })}
        >
          <HBarChart data={elevatedApps} valueLabel="elevations" roundValues height={300} />
        </ChartCard>
        <ChartCard
          title="Top Users by UAC Elevations"
          subtitle={elevationSubtitle}
          onViewAll={() => openViewAll({
            title: 'All Users by UAC Elevation Count', valueLabel: 'elevations', roundValues: true,
            fetcher: () => getTopUsersByElevations(loginRange, VIEW_ALL_LIMIT, filters).then(r => parsePromVector(r, 'user')),
          })}
        >
          <HBarChart data={elevatingUsers} valueLabel="elevations" roundValues height={300} />
        </ChartCard>
      </div>
      {viewAllModal}
    </div>
  );
}

function UtilizationChart({ range, filters }) {
  const [resp, setResp] = useState(null);
  const [mode, setMode] = useState('pct'); // 'pct' | 'count'

  useEffect(() => {
    setResp(null);
    getUtilizationOverTime(range, filters)
      .then(r => setResp(r))
      .catch(() => setResp(false));
  }, [range, filters]);

  const { chartData, labs, totals } = useMemo(() => {
    if (!resp?.series?.length) return { chartData: [], labs: [], totals: {} };
    const labs = resp.series.map(s => s.lab);
    const totals = {};
    const lookup = {};
    resp.series.forEach(s => {
      totals[s.lab] = s.total;
      lookup[s.lab] = {};
      s.data.forEach(p => { lookup[s.lab][p.t] = p.v; });
    });
    const allTs = [...new Set(resp.series.flatMap(s => s.data.map(p => p.t)))].sort((a, b) => a - b);
    const chartData = allTs.map(t => {
      const row = { t };
      labs.forEach(lab => {
        if (lookup[lab][t] != null) {
          const raw = lookup[lab][t];
          row[lab] = raw; // raw count stored; mode transforms for display
        }
      });
      return row;
    });
    return { chartData, labs, totals };
  }, [resp]);

  // Transform values based on mode before passing to Recharts
  const displayData = useMemo(() => {
    if (mode === 'pct') {
      return chartData.map(row => {
        const r = { t: row.t };
        labs.forEach(lab => {
          if (row[lab] != null) r[lab] = Math.round((row[lab] / totals[lab]) * 1000) / 10;
        });
        return r;
      });
    }
    return chartData;
  }, [chartData, labs, totals, mode]);

  const rangeSecs = range ? (
    range.endsWith('d') ? parseInt(range) * 86400 :
    range.endsWith('h') ? parseInt(range) * 3600 : 86400
  ) : 86400;

  const fmtTime = (t) => {
    const d = new Date(t * 1000);
    if (rangeSecs <= 86400) return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    if (rangeSecs <= 7 * 86400) return d.toLocaleDateString([], { month: 'short', day: 'numeric' }) + ' ' + d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    return d.toLocaleDateString([], { month: 'short', day: 'numeric' });
  };

  const toggleStyle = (active) => ({
    padding: '2px 10px', fontSize: 12, cursor: 'pointer', borderRadius: 4,
    border: '1px solid var(--border,#444)',
    background: active ? 'var(--accent,#1e90ff)' : 'transparent',
    color: active ? '#fff' : undefined,
  });

  if (resp === null) return <div className="loading" style={{ padding: '1rem' }}>Loading…</div>;
  if (resp === false) return <div style={{ padding: '1rem', color: 'var(--error,#e55353)' }}>Failed to load data.</div>;
  if (!chartData.length) return <div style={{ padding: '1rem', color: 'var(--text-dim)' }}>No data for this period.</div>;

  const maxCount = mode === 'count'
    ? Math.max(...labs.map(l => totals[l] ?? 0))
    : 100;

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 4, marginBottom: 8 }}>
        <button style={toggleStyle(mode === 'pct')} onClick={() => setMode('pct')}>%</button>
        <button style={toggleStyle(mode === 'count')} onClick={() => setMode('count')}>#</button>
      </div>
    <ResponsiveContainer width="100%" height={260}>
      <LineChart data={displayData} margin={{ top: 8, right: 16, left: 0, bottom: 8 }}>
        <CartesianGrid strokeDasharray="3 3" stroke="var(--border,#333)" />
        <XAxis
          dataKey="t"
          tickFormatter={fmtTime}
          tick={{ fill: 'var(--text-dim)', fontSize: 11 }}
          minTickGap={40}
        />
        <YAxis
          domain={[0, maxCount]}
          tickFormatter={v => mode === 'pct' ? `${v}%` : v}
          tick={{ fill: 'var(--text-dim)', fontSize: 11 }}
          width={42}
        />
        <Tooltip
          formatter={(v, name) => [mode === 'pct' ? `${v}%` : `${v} machines`, name]}
          labelFormatter={fmtTime}
          contentStyle={{ background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 6, fontSize: 12 }}
        />
        {labs.length > 1 && <Legend wrapperStyle={{ fontSize: 12, paddingTop: 4 }} />}
        {labs.map((lab, i) => (
          <Line
            key={lab}
            type="monotone"
            dataKey={lab}
            stroke={CHART_COLORS[i % CHART_COLORS.length]}
            dot={false}
            strokeWidth={2}
            connectNulls
          />
        ))}
      </LineChart>
    </ResponsiveContainer>
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
    <div style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}>
      <ChartCard title="Usage Hours by Lab">
        <HBarChart
          data={applyAppFilter(data, appFilter)}
          valueLabel="hours"
          height={Math.max(240, (data?.length ?? 5) * 36)}
        />
      </ChartCard>
      <ChartCard title="Machine Utilization Over Time" subtitle="% of machines with an active session">
        <UtilizationChart range={range} filters={filters} />
      </ChartCard>
    </div>
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
    getBottomAppsByLaunches(range, 10, filters).then(r => setBottomLaunches(parsePromVector(r, 'app', true))).catch(() => setBottomLaunches(false));
    getTopAppsByUsage(range, 10, filters).then(r => setTopForeground(parsePromVector(r))).catch(() => setTopForeground(false));
  }, [range, filters]);

  const [viewAllModal, openViewAll] = useViewAllModal();

  // When a lab (or short window) has fewer than 10 apps with any usage at
  // all, "top" and "bottom" 10 are the same apps — this is the same sparsity
  // sparseNote surfaces on the panel titles below, not a bug in either list.
  const topLaunchesSubtitle = sparseNote(topLaunches, 10, 'apps had usage');
  const topForegroundSubtitle = sparseNote(topForeground, 10, 'apps had usage');
  const bottomLaunchesSubtitle = sparseNote(bottomLaunches, 10, 'apps had usage');

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
        <ChartCard
          title="Most Launched Apps"
          subtitle={topLaunchesSubtitle}
          onViewAll={() => openViewAll({
            title: 'All Apps by Launch Count', valueLabel: 'launches', roundValues: true,
            fetcher: () => getTopAppsByLaunches(range, VIEW_ALL_LIMIT, filters).then(r => applyAppFilter(parsePromVector(r), appFilter)),
          })}
        >
          <HBarChart data={applyAppFilter(topLaunches, appFilter)} valueLabel="launches" roundValues height={300} onIgnore={onIgnore} />
        </ChartCard>
        <ChartCard
          title="Most Active Apps"
          subtitle={topForegroundSubtitle}
          onViewAll={() => openViewAll({
            title: 'All Apps by Active Time', valueLabel: 'hours',
            fetcher: () => getTopAppsByUsage(range, VIEW_ALL_LIMIT, filters).then(r => applyAppFilter(parsePromVector(r), appFilter)),
          })}
        >
          <HBarChart data={applyAppFilter(topForeground, appFilter)} valueLabel="hours" height={300} onIgnore={onIgnore} />
        </ChartCard>
      </div>
      <ChartCard
        title="Least Launched Apps (Underutilized)"
        subtitle={bottomLaunchesSubtitle}
        onViewAll={() => openViewAll({
          title: 'All Underutilized Apps by Launch Count', valueLabel: 'launches', roundValues: true,
          fetcher: () => getBottomAppsByLaunches(range, VIEW_ALL_LIMIT, filters).then(r => applyAppFilter(parsePromVector(r, 'app', true), appFilter)),
        })}
      >
        <HBarChart data={applyAppFilter(bottomLaunches, appFilter)} valueLabel="launches" roundValues height={300} onIgnore={onIgnore} />
      </ChartCard>
      {viewAllModal}
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
