import { useState, useEffect } from 'react';
import { getAgents, deleteAgent, assignAgentToLab, getLabs, forceAgentUpdate } from '../api';
import ResizableTable from '../components/Table';

export default function Agents() {
  const [agents, setAgents] = useState([]);
  const [labs, setLabs] = useState([]);
  const [error, setError] = useState(null);

  const load = () => {
    Promise.all([getAgents(), getLabs()])
      .then(([a, l]) => { setAgents(a); setLabs(l); })
      .catch(e => setError(e.message));
  };

  useEffect(load, []);

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
    if (!confirm(`Force update agent ${id}?`)) return;
    try {
      const result = await forceAgentUpdate(id);
      alert(`Update queued: ${result.message || 'Agent will update on next heartbeat'}`);
      load();
    } catch (e) {
      alert(`Error: ${e.message}`);
    }
  };

  if (error) return <div className="error">{error}</div>;

  return (
    <div>
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
              <td>
                <button className="btn-secondary" onClick={() => handleForceUpdate(a.id)}>Update</button>
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
