import { useState, useEffect } from 'react';
import { useParams, Link } from 'react-router-dom';
import { getAgent, getLab } from '../../api';
import { useGlobalFilters } from '../../hooks/useGlobalFilters';
import GlobalFilterBar from '../../components/GlobalFilterBar';
import EntityActivityPanels from '../../components/EntityActivityPanels';

// Per-host detail page — clicking a hostname anywhere (Agents list) lands
// here instead of nowhere. Scope is already fixed to this one machine, so
// the shared filter bar only exposes Time Range (no Machine/Lab picker).
export default function AgentDetail() {
  const { id } = useParams();
  const [agent, setAgent] = useState(null);
  const [agentError, setAgentError] = useState(false);
  const [lab, setLab] = useState(null);

  const globalFilters = useGlobalFilters();
  const { range, isCustomReady, effectiveRange } = globalFilters;
  const ready = range !== 'custom' || isCustomReady;

  useEffect(() => {
    setAgent(null);
    setAgentError(false);
    getAgent(id).then(setAgent).catch(() => setAgentError(true));
  }, [id]);

  useEffect(() => {
    if (!agent?.labId) { setLab(null); return; }
    getLab(agent.labId).then(setLab).catch(() => setLab(null));
  }, [agent?.labId]);

  if (agentError) return <div className="error">Failed to load agent.</div>;
  if (!agent) return <div className="loading">Loading…</div>;

  return (
    <div>
      <Link to="/agents" style={{ fontSize: 13, color: 'var(--text-dim)' }}>← Back to Agents</Link>

      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: '0.75rem', marginTop: '0.5rem' }}>
        <h2 style={{ margin: 0 }}>{agent.hostname}</h2>
        <span className={`badge ${agent.status}`}>{agent.status}</span>
      </div>

      <div className="stats-grid" style={{ marginTop: '1rem' }}>
        <div className="stat-card">
          <span className="stat-value" style={{ fontSize: '1.1rem' }}>{agent.ipAddress || '—'}</span>
          <span className="stat-label">IP Address</span>
        </div>
        <div className="stat-card">
          <span className="stat-value" style={{ fontSize: '1.1rem' }}>{agent.osVersion || '—'}</span>
          <span className="stat-label">OS Version</span>
        </div>
        <div className="stat-card">
          <span className="stat-value" style={{ fontSize: '1.1rem' }}>{agent.agentVersion || '—'}</span>
          <span className="stat-label">Agent Version</span>
        </div>
        <div className="stat-card">
          <span className="stat-value" style={{ fontSize: '1.1rem' }}>
            {lab ? <Link to={`/labs/${lab.id}`}>{lab.name}</Link> : 'Unassigned'}
          </span>
          <span className="stat-label">Lab</span>
        </div>
        <div className="stat-card">
          <span className="stat-value" style={{ fontSize: '1.1rem' }}>
            {agent.lastSeen ? new Date(agent.lastSeen).toLocaleString() : 'Never'}
          </span>
          <span className="stat-label">Last Seen</span>
        </div>
      </div>

      <GlobalFilterBar filters={globalFilters} showMachine={false} showLab={false} />

      {range === 'custom' && !isCustomReady && (
        <div className="warning-banner">
          Select a valid start and end time to load data.
        </div>
      )}

      <EntityActivityPanels filters={{ hostname: id }} range={effectiveRange} ready={ready} />
    </div>
  );
}
