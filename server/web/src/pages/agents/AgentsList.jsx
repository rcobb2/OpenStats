import { useState, useEffect, useMemo } from 'react';
import { Link } from 'react-router-dom';
import { getAgents, deleteAgent, assignAgentToLab, getLabs, forceAgentUpdate } from '../../api';
import ResizableTable from '../../components/Table';
// Per-row lab assignment intentionally stays a plain <select>, not
// FilterableSelect — with 175+ labs, an inline filter box repeated across
// every one of 739 rows would make the table far taller and busier than it
// already is. A bulk/modal-based assignment UI would be the right fix for
// this specific case; flagged as a follow-up rather than done here.

export default function AgentsList() {
  const [agents, setAgents] = useState([]);
  const [labs, setLabs] = useState([]);
  const [error, setError] = useState(null);
  const [updating, setUpdating] = useState({});
  const [toast, setToast] = useState(null);
  const [loading, setLoading] = useState(true);
  const [sort, setSort] = useState({ key: 'hostname', dir: 'asc' });
  const [statusFilter, setStatusFilter] = useState('all');
  const [filter, setFilter] = useState('');

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
    if (!confirm(`Force update agent ${id}?\n\nThis bypasses the maintenance window and rollout throttle — the agent will install immediately on its next heartbeat.`)) return;
    setUpdating(u => ({ ...u, [id]: true }));
    try {
      await forceAgentUpdate(id);
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

  const statusCounts = useMemo(() => {
    const counts = { online: 0, outdated: 0, offline: 0 };
    for (const a of agents) {
      if (counts[a.status] !== undefined) counts[a.status]++;
    }
    return counts;
  }, [agents]);

  const sortedAgents = useMemo(() => {
    let rows = agents;
    if (statusFilter !== 'all') rows = rows.filter(a => a.status === statusFilter);
    if (filter) {
      const q = filter.toLowerCase();
      rows = rows.filter(a =>
        a.hostname?.toLowerCase().includes(q) ||
        a.ipAddress?.toLowerCase().includes(q) ||
        labName(a).toLowerCase().includes(q)
      );
    }
    rows = [...rows];
    rows.sort((a, b) => {
      const av = sortValue(a, sort.key);
      const bv = sortValue(b, sort.key);
      let cmp;
      if (typeof av === 'number' && typeof bv === 'number') cmp = av - bv;
      else cmp = String(av).localeCompare(String(bv), undefined, { numeric: true, sensitivity: 'base' });
      return sort.dir === 'asc' ? cmp : -cmp;
    });
    return rows;
  }, [agents, labs, sort, statusFilter, filter]);

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
  if (loading) return <div className="loading">Loading agents…</div>;

  return (
    <div>
      {toast && (
        <div className={`toast-banner ${toast.type}`} style={{
          position: 'fixed', top: '1.5rem', right: '1.5rem', zIndex: 9999,
          padding: '0.85rem 1.5rem', borderRadius: '10px', fontWeight: 500,
          background: toast.type === 'error' ? 'var(--danger)' : 'var(--success)',
          color: '#fff', boxShadow: '0 4px 18px rgba(0,0,0,0.18)',
          animation: 'fadeIn 0.2s ease'
        }}>{toast.msg}</div>
      )}

      <h2>Agents ({agents.length})</h2>

      <div style={{ display: 'flex', gap: '0.75rem', marginBottom: '0.75rem', alignItems: 'center', flexWrap: 'wrap' }}>
        <div className="tab-bar" style={{ marginBottom: 0 }}>
          <button className={`tab ${statusFilter === 'all' ? 'active' : ''}`} onClick={() => setStatusFilter('all')}>
            All <span className="badge">{agents.length}</span>
          </button>
          <button className={`tab ${statusFilter === 'online' ? 'active' : ''}`} onClick={() => setStatusFilter('online')}>
            Online
            {statusCounts.online > 0 && <span className="badge online" style={{ marginLeft: '0.4em' }}>{statusCounts.online}</span>}
          </button>
          <button className={`tab ${statusFilter === 'outdated' ? 'active' : ''}`} onClick={() => setStatusFilter('outdated')}>
            Outdated
            {statusCounts.outdated > 0 && <span className="badge outdated" style={{ marginLeft: '0.4em' }}>{statusCounts.outdated}</span>}
          </button>
          <button className={`tab ${statusFilter === 'offline' ? 'active' : ''}`} onClick={() => setStatusFilter('offline')}>
            Offline
            {statusCounts.offline > 0 && <span className="badge offline" style={{ marginLeft: '0.4em' }}>{statusCounts.offline}</span>}
          </button>
        </div>
        <input
          className="search"
          placeholder="Filter by hostname, IP, or lab..."
          value={filter}
          onChange={e => setFilter(e.target.value)}
          style={{ marginLeft: 'auto', width: '240px' }}
        />
      </div>

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
              <td><Link to={`/agents/${a.id}`}>{a.hostname}</Link></td>
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
      {agents.length > 0 && sortedAgents.length === 0 && <p className="empty">No agents match the current filter.</p>}
    </div>
  );
}
