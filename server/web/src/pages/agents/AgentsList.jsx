import { useState, useEffect } from 'react';
import { getAgents, deleteAgent, assignAgentToLab, getLabs, forceAgentUpdate } from '../../api';
import ResizableTable from '../../components/Table';

export default function AgentsList() {
  const [agents, setAgents] = useState([]);
  const [labs, setLabs] = useState([]);
  const [error, setError] = useState(null);
  const [updating, setUpdating] = useState({});
  const [toast, setToast] = useState(null);

  const load = () => {
    Promise.all([getAgents(), getLabs()])
      .then(([a, l]) => { setAgents(a); setLabs(l); })
      .catch(e => setError(e.message));
  };

  useEffect(load, []);

  const showToast = (msg, type = 'success') => {
    setToast({ msg, type });
    setTimeout(() => setToast(null), 4000);
  };

  const handleDelete = async (id) => {
    if (!confirm(`Remove agent ${id}?`)) return;
    await deleteAgent(id);
    load();
  };

  const handleAssignLab = async (agentId, labId) => {
    await assignAgentToLab(agentId, labId);
    load();
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
            <th>Hostname</th>
            <th>IP</th>
            <th>Status</th>
            <th>Version</th>
            <th>Lab</th>
            <th>Last Seen</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {agents.map(a => (
            <tr key={a.id}>
              <td>{a.hostname}</td>
              <td>{a.ipAddress}</td>
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
              <td>{new Date(a.lastSeen).toLocaleString()}</td>
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
