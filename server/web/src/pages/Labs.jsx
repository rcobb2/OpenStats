import { useState, useEffect } from 'react';
import { Link } from 'react-router-dom';
import { getLabs, createLab, updateLab, deleteLab } from '../api';
import ResizableTable from '../components/Table';

export default function Labs() {
  const [labs, setLabs] = useState([]);
  const [form, setForm] = useState({ name: '', building: '', room: '', description: '' });
  const [editingId, setEditingId] = useState(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);

  const load = () => {
    setError(''); setLoading(true);
    getLabs().then(setLabs).catch(() => setError('Failed to load labs.')).finally(() => setLoading(false));
  };
  useEffect(() => { load(); }, []);

  const handleSubmit = async (e) => {
    e.preventDefault();
    if (!form.name) return;
    setError('');
    try {
      if (editingId) {
        await updateLab(editingId, form);
      } else {
        await createLab(form);
      }
      handleCancel();
      load();
    } catch {
      setError(editingId ? 'Failed to update lab.' : 'Failed to create lab.');
    }
  };

  const handleEdit = (lab) => {
    setEditingId(lab.id);
    setForm({
      name: lab.name,
      building: lab.building,
      room: lab.room,
      description: lab.description
    });
  };

  const handleCancel = () => {
    setEditingId(null);
    setForm({ name: '', building: '', room: '', description: '' });
  };

  const handleDelete = async (id) => {
    if (!confirm('Delete this lab?')) return;
    setError('');
    try {
      await deleteLab(id);
      load();
    } catch {
      setError('Failed to delete lab.');
    }
  };

  return (
    <div>
      <h2>Labs & Rooms</h2>

      {error && <div className="error-banner">{error}</div>}

      <div className="card" style={{ marginBottom: '1.5rem' }}>
        <h3 style={{ marginTop: 0 }}>{editingId ? 'Edit Lab' : 'Add New Lab'}</h3>
        <form onSubmit={handleSubmit} className="form-inline">
          <input placeholder="Name (e.g. Library 101)" value={form.name}
            onChange={e => setForm({ ...form, name: e.target.value })} required />
          <input placeholder="Building" value={form.building}
            onChange={e => setForm({ ...form, building: e.target.value })} />
          <input placeholder="Room" value={form.room}
            onChange={e => setForm({ ...form, room: e.target.value })} />
          <input placeholder="Description" value={form.description}
            onChange={e => setForm({ ...form, description: e.target.value })} />
          <button type="submit">{editingId ? 'Update' : 'Add'}</button>
          {editingId && (
            <button type="button" className="btn-secondary" onClick={handleCancel}>
              Cancel
            </button>
          )}
        </form>
      </div>

      {loading ? (
        <div className="loading">Loading labs…</div>
      ) : (
      <ResizableTable>
        <thead>
          <tr><th>Name</th><th>Building</th><th>Room</th><th>Description</th><th style={{ width: '150px' }}>Actions</th></tr>
        </thead>
        <tbody>
          {labs.map(l => (
            <tr key={l.id} style={editingId === l.id ? { background: 'var(--accent-soft)' } : {}}>
              <td><Link to={`/labs/${l.id}`}>{l.name}</Link></td>
              <td>{l.building}</td>
              <td>{l.room}</td>
              <td>{l.description}</td>
              <td>
                <div style={{ display: 'flex', gap: '0.5rem' }}>
                  <button onClick={() => handleEdit(l)} style={{ padding: '0.3rem 0.6rem', fontSize: '0.8rem' }}>Edit</button>
                  <button className="btn-danger" onClick={() => handleDelete(l.id)}>Delete</button>
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </ResizableTable>
      )}
      {!loading && labs.length === 0 && <p className="empty">No labs configured yet.</p>}
    </div>
  );
}

