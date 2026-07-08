import { useState, useEffect, useCallback } from 'react';
import { getMappings, createMapping, updateMapping, deleteMapping, patchMappingIgnore } from '../api';
import ResizableTable from '../components/Table';

const EMPTY_FORM = { exeName: '', displayName: '', category: '', publisher: '', family: '' };

export default function Mappings() {
  const [mappings, setMappings] = useState([]);
  const [tab, setTab] = useState('all');
  const [filter, setFilter] = useState('');
  const [form, setForm] = useState(EMPTY_FORM);
  const [showAddForm, setShowAddForm] = useState(false);
  const [editId, setEditId] = useState(null);
  const [editForm, setEditForm] = useState(EMPTY_FORM);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  const load = useCallback(() =>
    getMappings().then(data => setMappings(data || [])).catch(() => setError('Failed to load mappings.')),
    []
  );

  useEffect(() => { load(); }, [load]);

  const reviewCount = mappings.filter(m => m.source === 'auto' && !m.ignored).length;
  const ignoredCount = mappings.filter(m => m.ignored).length;

  const tabFiltered = mappings.filter(m => {
    if (tab === 'review') return m.source === 'auto' && !m.ignored;
    if (tab === 'ignored') return m.ignored;
    return true;
  });

  const filtered = tabFiltered.filter(m =>
    !filter ||
    m.exeName.toLowerCase().includes(filter.toLowerCase()) ||
    m.displayName.toLowerCase().includes(filter.toLowerCase())
  );

  const handleAdd = async (e) => {
    e.preventDefault();
    if (!form.exeName || !form.displayName) return;
    setError(''); setSaving(true);
    try {
      await createMapping({ ...form, ignored: false });
      setForm(EMPTY_FORM);
      setShowAddForm(false);
      load();
    } catch {
      setError('Failed to create mapping.');
    } finally { setSaving(false); }
  };

  const startEdit = (m) => {
    setEditId(m.id);
    setEditForm({
      exeName: m.exeName,
      displayName: m.displayName,
      category: m.category,
      publisher: m.publisher,
      family: m.family || '',
    });
  };

  const handleUpdate = async (e, m) => {
    e.preventDefault();
    setError(''); setSaving(true);
    try {
      await updateMapping({ ...editForm, ignored: m.ignored });
      setEditId(null);
      load();
    } catch {
      setError('Failed to update mapping.');
    } finally { setSaving(false); }
  };

  const handleToggleIgnore = async (m) => {
    setError(''); setSaving(true);
    try {
      await patchMappingIgnore(m.id, !m.ignored);
      await load();
    } catch {
      setError('Failed to update mapping.');
    } finally { setSaving(false); }
  };

  const handleDelete = async (id) => {
    if (!confirm('Delete this mapping?')) return;
    setError('');
    try {
      await deleteMapping(id);
      load();
    } catch { setError('Failed to delete mapping.'); }
  };

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', gap: '1rem', marginBottom: '1rem' }}>
        <h2 style={{ margin: 0 }}>Software Mappings</h2>
        <button
          onClick={() => { setShowAddForm(v => !v); setError(''); }}
          style={{ marginLeft: 'auto', padding: '0.35rem 0.85rem', cursor: 'pointer' }}
        >
          {showAddForm ? 'Cancel' : '+ Add Mapping'}
        </button>
      </div>

      {error && <div style={{ color: 'var(--error, #e55353)', marginBottom: '1rem' }}>{error}</div>}

      {showAddForm && (
        <form onSubmit={handleAdd} className="form-inline" style={{ marginBottom: '1.25rem' }}>
          <input placeholder="Exe name (e.g. EXCEL.EXE)" value={form.exeName}
            onChange={e => setForm({ ...form, exeName: e.target.value })} required />
          <input placeholder="Display name" value={form.displayName}
            onChange={e => setForm({ ...form, displayName: e.target.value })} required />
          <input placeholder="Category" value={form.category}
            onChange={e => setForm({ ...form, category: e.target.value })} />
          <input placeholder="Publisher" value={form.publisher}
            onChange={e => setForm({ ...form, publisher: e.target.value })} />
          <input placeholder="Family key" value={form.family}
            onChange={e => setForm({ ...form, family: e.target.value })} />
          <button type="submit" disabled={saving}>Add</button>
        </form>
      )}

      <div style={{ display: 'flex', gap: '0.5rem', marginBottom: '0.75rem', alignItems: 'center', flexWrap: 'wrap' }}>
        <TabButton active={tab === 'all'} onClick={() => setTab('all')}>
          All <span className="badge">{mappings.length}</span>
        </TabButton>
        <TabButton active={tab === 'review'} onClick={() => setTab('review')}>
          Needs Review
          {reviewCount > 0 && <span className="badge" style={{ marginLeft: '0.4em', background: '#e67e22', color: '#fff' }}>{reviewCount}</span>}
        </TabButton>
        <TabButton active={tab === 'ignored'} onClick={() => setTab('ignored')}>
          Ignored
          {ignoredCount > 0 && <span className="badge" style={{ marginLeft: '0.4em' }}>{ignoredCount}</span>}
        </TabButton>
        <input
          className="search"
          placeholder="Filter..."
          value={filter}
          onChange={e => setFilter(e.target.value)}
          style={{ marginLeft: 'auto', width: '200px' }}
        />
      </div>

      {tab === 'review' && reviewCount > 0 && (
        <div style={{ padding: '0.5rem 0.75rem', marginBottom: '0.75rem', borderRadius: '4px', background: 'var(--surface2, rgba(255,255,255,0.05))', fontSize: '0.875rem', color: 'var(--text-muted, #aaa)' }}>
          {reviewCount} process{reviewCount !== 1 ? 'es' : ''} auto-discovered. Edit to set a friendly display name, or ignore junk processes to drop them from metrics.
        </div>
      )}

      <ResizableTable>
        <thead>
          <tr>
            <th>Exe Name</th>
            <th>Display Name</th>
            <th>Category</th>
            <th>Publisher</th>
            <th>Source</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {filtered.map(m => editId === m.id ? (
            <tr key={m.id}>
              <td><code>{m.exeName}</code></td>
              <td>
                <input
                  value={editForm.displayName}
                  onChange={e => setEditForm({ ...editForm, displayName: e.target.value })}
                  style={{ width: '100%' }}
                  autoFocus
                />
              </td>
              <td>
                <input
                  value={editForm.category}
                  onChange={e => setEditForm({ ...editForm, category: e.target.value })}
                  style={{ width: '100%' }}
                />
              </td>
              <td>
                <input
                  value={editForm.publisher}
                  onChange={e => setEditForm({ ...editForm, publisher: e.target.value })}
                  style={{ width: '100%' }}
                />
              </td>
              <td><span className="badge">{m.source}</span></td>
              <td>
                <div style={{ display: 'flex', gap: '0.4rem' }}>
                  <button onClick={(e) => handleUpdate(e, m)} disabled={saving}>Save</button>
                  <button onClick={() => setEditId(null)}>Cancel</button>
                </div>
              </td>
            </tr>
          ) : (
            <tr key={m.id} style={m.ignored ? { opacity: 0.45 } : undefined}>
              <td><code>{m.exeName}</code></td>
              <td>{m.displayName}</td>
              <td>{m.category}</td>
              <td>{m.publisher}</td>
              <td>
                <span
                  className="badge"
                  style={m.source === 'auto' ? { background: '#e67e22', color: '#fff' } : undefined}
                >
                  {m.source}
                </span>
              </td>
              <td>
                <div style={{ display: 'flex', gap: '0.4rem', flexWrap: 'wrap' }}>
                  <button onClick={() => startEdit(m)}>Edit</button>
                  <button
                    onClick={() => handleToggleIgnore(m)}
                    title={m.ignored ? 'Remove from ignored list' : 'Ignore — drop from metrics'}
                    style={m.ignored ? { borderColor: 'var(--accent, #1e90ff)' } : undefined}
                  >
                    {m.ignored ? 'Unignore' : 'Ignore'}
                  </button>
                  <button className="btn-danger" onClick={() => handleDelete(m.id)}>Delete</button>
                </div>
              </td>
            </tr>
          ))}
          {filtered.length === 0 && (
            <tr><td colSpan={6} style={{ textAlign: 'center', color: 'var(--text-muted, #aaa)', padding: '1.5rem' }}>
              {filter ? 'No mappings match the filter.' : tab === 'review' ? 'No auto-discovered processes to review.' : tab === 'ignored' ? 'No ignored processes.' : 'No mappings yet.'}
            </td></tr>
          )}
        </tbody>
      </ResizableTable>
    </div>
  );
}

function TabButton({ active, onClick, children }) {
  return (
    <button
      onClick={onClick}
      style={{
        padding: '0.35rem 0.75rem',
        border: '1px solid var(--border, #444)',
        borderRadius: '4px',
        cursor: 'pointer',
        background: active ? 'var(--accent, #1e90ff)' : 'transparent',
        color: active ? '#fff' : undefined,
      }}
    >
      {children}
    </button>
  );
}
