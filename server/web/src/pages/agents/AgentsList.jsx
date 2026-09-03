import { useState, useEffect, useMemo } from 'react';
import { getAgents, deleteAgent, assignAgentToLab, getLabs, forceAgentUpdate } from '../../api';
import ResizableTable from '../../components/Table';

export default function AgentsList() {
  const [agents, setAgents] = useState([]);
  const [labs, setLabs] = useState([]);
  const [error, setError] = useState(null);
  const [updating, setUpdating] = useState({});
  const [toast, setToast] = useState(null);
  const [loading, setLoading] = useState(true);
  const [sort, setSort] = useState({ key: 'hostname', dir: 'asc' });

  const load = () => {
    setError(null); setLoading(true);
    Promise.all([getAgents(), getLabs()])
      .then(([a, l]) => { setAgents(a); setLabs(l); })
      .catch(e => setError(e.message))
      .finally(() => setLoading(false));
  };

  useEffect(load, []);

  const showToast = (msg, type = 'success') => {
    setToast({ msg, type });
    setTimeout(() => setToast(null), 4000);
  };

  const handleDelete = async (id) => {
    if (!confirm(`Remove agent ${id}?`)) return;
    try {
      await deleteAgent(id);
      load();
    } catch (err) {
      showToast(`✗ Failed to remove agent: ${err.message}`, 'error');
    }
  };

  const handleAssignLab = async (agentId, labId) => {
    try {
      await assignAgentToLab(agentId, labId);
      load();
    } catch (err) {
      showToast(`✗ Failed to assign lab: ${err.message}`, 'error');
    }
  };

  const handleForceUpdate = async (id) => {
    if (!confirm(`Force update agent ${id}?\n\nThe agent will receive the update URL on its next heartbeat and install within its maintenance window.`)) return;
    setUpdating(u => ({ ...u, [id]: true }));
    try {
      const res = await forceAgentUpdate(id);
      showToast(`✓ Update queued for ${id}. The agent will install on next heartbeat.`);
      load();
    } catch (err) {
      showToast(`✗ Failed to queue update: ${err.message}`, 'error');
    } finally {
      setUpdating(u => ({ ...u, [id]: false }));
    }
  };

  const labName = (a) => {
    const l = labs.find(l => l.id === a.labId);
    return l ? l.name : '';
  };

  // Value used to compare a row for each sortable column.
  const sortValue = (a, key) => {
    switch (key) {
      case 'hostname': return a.hostname || '';
      case 'ipAddress': return a.ipAddress || '';
      case 'osVersion': return a.osVersion || '';
      case 'status': return a.status || '';
      case 'agentVersion': return a.agentVersion || '';
      case 'lab': return labName(a);
      case 'lastSeen': return a.lastSeen ? new Date(a.lastSeen).getTime() : 0;
      default: return '';
    }
  };

  const sortedAgents = useMemo(() => {
    const rows = [...agents];
    rows.sort((a, b) => {
      const av = sortValue(a, sort.key);
      const bv = sortValue(b, sort.key);
      let cmp;
      if (typeof av === 'number' && typeof bv === 'number') cmp = av - bv;
      else cmp = String(av).localeCompare(String(bv), undefined, { numeric: true, sensitivity: 'base' });
      return sort.dir === 'asc' ? cmp : -cmp;
    });
    return rows;
  }, [agents, labs, sort]);

  const toggleSort = (key) => {
    setSort(s => s.key === key
      ? { key, dir: s.dir === 'asc' ? 'desc' : 'asc' }
      : { key, dir: 'asc' });
  };

  const SortHeader = ({ label, sortKey }) => (
    <th onClick={() => toggleSort(sortKey)} style={{ cursor: 'pointer', userSelect: 'none' }}>
      {label}
      <span style={{ opacity: sort.key === sortKey ? 0.9 : 0.25, marginLeft: '0.35em', fontSize: '0.8em' }}>
        {sort.key === sortKey ? (sort.dir === 'asc' ? '▲' : '▼') : '▲'}
      </span>
    </th>
  );

  if (error) return <div className="error">{error}</div>;

  return (
    <div>
      {toast && (
        <div className={`toast-banner ${toast.type}`} style={{
          position: 'fixed', top: '1.5rem', right: '1.5rem', zIndex: 9999,
          padding: '0.85rem 1.5rem', borderRadius: '10px', fontWeight: 500,
          background: toast.type === 'error' ? 'var(--danger, #e74c3c)' : 'var(--success, #27ae60)',
          color: '#fff', boxShadow: '0 4px 18px rgba(0,0,0,0.18)',
          animation: 'fadeIn 0.2s ease'
        }}>{toast.msg}</div>
      )}

      <h2>Agents ({agents.length})</h2>
      <ResizableTable>
        <thead>
          <tr>
            <SortHeader label="Hostname" sortKey="hostname" />
            <SortHeader label="IP" sortKey="ipAddress" />
            <SortHeader label="OS Version" sortKey="osVersion" />
            <SortHeader label="Status" sortKey="status" />
            <SortHeader label="Agent Ver." sortKey="agentVersion" />
            <SortHeader label="Lab" sortKey="lab" />
            <SortHeader label="Last Seen" sortKey="lastSeen" />
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {sortedAgents.map(a => (
            <tr key={a.id}>
              <td>{a.hostname}</td>
              <td>{a.ipAddress}</td>
              <td style={{ fontSize: '0.85em', color: 'var(--text-dim)' }}>{a.osVersion || '—'}</td>
              <td><span className={`badge ${a.status}`}>{a.status}</span></td>
              <td>{a.agentVersion}</td>
              <td>
                <select
                  value={a.labId || ''}
                  onChange={e => handleAssignLab(a.id, e.target.value)}
                >
                  <option value="">Unassigned</option>
                  {labs.map(l => (
                    <option key={l.id} value={l.id}>
                      {l.name} {l.building || l.room ? `(${[l.building, l.room].filter(Boolean).join(' - ')})` : ''}
                    </option>
                  ))}
                </select>
              </td>
              <td>{a.lastSeen ? new Date(a.lastSeen).toLocaleString() : 'Never'}</td>
              <td style={{ display: 'flex', gap: '0.4rem', flexWrap: 'wrap' }}>
                {(a.status === 'outdated' || a.status === 'online') && (
                  <button
                    className="btn-warning"
                    title="Force the agent to download and install the latest version"
                    onClick={() => handleForceUpdate(a.id)}
                    disabled={updating[a.id]}
                    style={{
                      background: 'linear-gradient(135deg, #f39c12, #e67e22)',
                      color: '#fff', border: 'none', borderRadius: '6px',
                      padding: '0.3rem 0.7rem', cursor: 'pointer', fontSize: '0.82rem',
                      opacity: updating[a.id] ? 0.6 : 1
                    }}
                  >
                    {updating[a.id] ? '⏳ Queuing…' : '⬆ Force Update'}
                  </button>
                )}
                <button className="btn-danger" onClick={() => handleDelete(a.id)}>Remove</button>
              </td>
            </tr>
          ))}
        </tbody>
      </ResizableTable>
      {agents.length === 0 && <p className="empty">No agents enrolled yet. Install the agent on lab machines to get started.</p>}
    </div>
  );
}
