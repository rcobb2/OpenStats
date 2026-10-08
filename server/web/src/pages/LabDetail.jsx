import { useState, useEffect, useMemo } from 'react';
import { useParams, Link } from 'react-router-dom';
import { getLab, getAgents } from '../api';
import { useGlobalFilters } from '../hooks/useGlobalFilters';
import GlobalFilterBar from '../components/GlobalFilterBar';
import EntityActivityPanels from '../components/EntityActivityPanels';
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

  useEffect(() => {
    setLab(null);
    setLabError(false);
    getLab(id).then(setLab).catch(() => setLabError(true));
  }, [id]);

  useEffect(() => {
    getAgents().then(setAgents).catch(() => {});
  }, []);

  const labAgents = useMemo(() => agents.filter(a => a.labId === id), [agents, id]);

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

      <EntityActivityPanels filters={{ lab: lab.name }} range={effectiveRange} ready={ready} />

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
