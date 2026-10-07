import { useState, useEffect, useMemo } from 'react';
import { useParams, Link } from 'react-router-dom';
import {
  getLab, getAgents, getTopAppsByForeground, getTopAppsByLaunches,
  getTopUsersByLogins, getTopAppsByElevations, parsePromVector,
} from '../api';
import { useGlobalFilters } from '../hooks/useGlobalFilters';
import GlobalFilterBar from '../components/GlobalFilterBar';
import MiniBarList from '../components/MiniBarList';
import ResizableTable from '../components/Table';

// Per-lab detail page — clicking a lab name anywhere (Labs list) lands here
// instead of nowhere. Scope is already fixed to this one lab, so the shared
// filter bar only exposes Time Range.
export default function LabDetail() {
  const { id } = useParams();
  const [lab, setLab] = useState(null);
  const [labError, setLabError] = useState(false);
  const [agents, setAgents] = useState([]);

  const globalFilters = useGlobalFilters();
  const { range, isCustomReady, effectiveRange } = globalFilters;
  const ready = range !== 'custom' || isCustomReady;

  const [foreground, setForeground] = useState(null);
  const [foregroundError, setForegroundError] = useState(false);
  const [launches, setLaunches] = useState(null);
  const [launchesError, setLaunchesError] = useState(false);
  const [logins, setLogins] = useState(null);
  const [loginsError, setLoginsError] = useState(false);
  const [elevations, setElevations] = useState(null);
  const [elevationsError, setElevationsError] = useState(false);

  useEffect(() => {
    setLab(null);
    setLabError(false);
    getLab(id).then(setLab).catch(() => setLabError(true));
  }, [id]);

  useEffect(() => {
    getAgents().then(setAgents).catch(() => {});
  }, []);

  const labAgents = useMemo(() => agents.filter(a => a.labId === id), [agents, id]);

  useEffect(() => {
    if (!ready || !lab) return;
    const lf = { lab: lab.name };

    setForeground(null); setForegroundError(false);
    getTopAppsByForeground(effectiveRange, 10, lf)
      .then(r => setForeground(parsePromVector(r))).catch(() => setForegroundError(true));

    setLaunches(null); setLaunchesError(false);
    getTopAppsByLaunches(effectiveRange, 10, lf)
      .then(r => setLaunches(parsePromVector(r))).catch(() => setLaunchesError(true));

    setLogins(null); setLoginsError(false);
    getTopUsersByLogins(effectiveRange, 10, lf)
      .then(r => setLogins(parsePromVector(r, 'user'))).catch(() => setLoginsError(true));

    setElevations(null); setElevationsError(false);
    getTopAppsByElevations(effectiveRange, 10, lf)
      .then(r => setElevations(parsePromVector(r))).catch(() => setElevationsError(true));
  }, [lab, effectiveRange, ready]);

  if (labError) return <div className="error">Failed to load lab.</div>;
  if (!lab) return <div className="loading">Loading…</div>;

  return (
    <div>
      <Link to="/labs" style={{ fontSize: 13, color: 'var(--text-dim)' }}>← Back to Labs</Link>

      <h2 style={{ marginTop: '0.5rem' }}>{lab.name}</h2>
      {(lab.building || lab.room) && (
        <div style={{ color: 'var(--text-dim)', fontSize: '0.9rem', marginTop: '-0.5rem', marginBottom: '1rem' }}>
          {[lab.building, lab.room].filter(Boolean).join(' · ')}
        </div>
      )}
      {lab.description && <p style={{ color: 'var(--text-dim)' }}>{lab.description}</p>}

      <GlobalFilterBar filters={globalFilters} showMachine={false} showLab={false} />

      {range === 'custom' && !isCustomReady && (
        <div className="warning-banner">
          Select a valid start and end time to load data.
        </div>
      )}

      {ready && (
        <div className="panel-grid">
          <div className="chart-card">
            <h3 style={{ marginTop: 0, marginBottom: '1rem' }}>Most Active Apps</h3>
            <MiniBarList
              data={foreground} error={foregroundError}
              formatValue={v => `${v.toFixed(1)}h`}
              emptyMessage="No app usage in this period."
            />
          </div>
          <div className="chart-card">
            <h3 style={{ marginTop: 0, marginBottom: '1rem' }}>Most Launched Apps</h3>
            <MiniBarList data={launches} error={launchesError} emptyMessage="No launches in this period." />
          </div>
          <div className="chart-card">
            <h3 style={{ marginTop: 0, marginBottom: '1rem' }}>Users Signed In</h3>
            <MiniBarList data={logins} error={loginsError} emptyMessage="No logins in this period." />
          </div>
          <div className="chart-card">
            <h3 style={{ marginTop: 0, marginBottom: '1rem' }}>Privilege Elevations</h3>
            <MiniBarList data={elevations} error={elevationsError} emptyMessage="No privilege elevations in this period." />
          </div>
        </div>
      )}

      <h3 style={{ marginTop: '1.5rem' }}>Agents in this Lab ({labAgents.length})</h3>
      {labAgents.length === 0 ? (
        <p className="empty">No agents assigned to this lab yet.</p>
      ) : (
        <ResizableTable>
          <thead>
            <tr>
              <th>Hostname</th>
              <th>IP</th>
              <th>Status</th>
              <th>Last Seen</th>
            </tr>
          </thead>
          <tbody>
            {labAgents.map(a => (
              <tr key={a.id}>
                <td><Link to={`/agents/${a.id}`}>{a.hostname}</Link></td>
                <td>{a.ipAddress}</td>
                <td><span className={`badge ${a.status}`}>{a.status}</span></td>
                <td>{a.lastSeen ? new Date(a.lastSeen).toLocaleString() : 'Never'}</td>
              </tr>
            ))}
          </tbody>
        </ResizableTable>
      )}
    </div>
  );
}
