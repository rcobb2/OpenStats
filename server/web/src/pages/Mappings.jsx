import { useState, useEffect } from 'react';
import { getMappings, createMapping, deleteMapping } from '../api';
import ResizableTable from '../components/Table';

export default function Mappings() {
  const [mappings, setMappings] = useState([]);
  const [filter, setFilter] = useState('');
  const [form, setForm] = useState({ exeName: '', displayName: '', category: '', publisher: '', family: '' });

  const load = () => getMappings().then(setMappings);
  useEffect(() => { load(); }, []);

  const handleSubmit = async (e) => {
    e.preventDefault();
    if (!form.exeName || !form.displayName) return;
    await createMapping(form);
    setForm({ exeName: '', displayName: '', category: '', publisher: '', family: '' });
    load();
  };

  const handleDelete = async (id) => {
    if (!confirm('Delete this mapping?')) return;
    await deleteMapping(id);
    load();
  };

  const filtered = mappings.filter(m =>
    m.exeName.toLowerCase().includes(filter.toLowerCase()) ||
    m.displayName.toLowerCase().includes(filter.toLowerCase())
  );

  return (
    <div>
      <h2>Software Mappings ({mappings.length})</h2>

      <form onSubmit={handleSubmit} className="form-inline">
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
        <button type="submit">Add</button>
      </form>

      <input className="search" placeholder="Filter mappings..." value={filter}
        onChange={e => setFilter(e.target.value)} />

      <ResizableTable>
        <thead>
          <tr><th>Exe Name</th><th>Display Name</th><th>Category</th><th>Publisher</th><th>Family</th><th>Source</th><th>Actions</th></tr>
        </thead>
        <tbody>
          {filtered.map(m => (
            <tr key={m.id}>
              <td><code>{m.exeName}</code></td>
              <td>{m.displayName}</td>
              <td>{m.category}</td>
              <td>{m.publisher}</td>
              <td>{m.family}</td>
              <td><span className="badge">{m.source}</span></td>
              <td><button className="btn-danger" onClick={() => handleDelete(m.id)}>Delete</button></td>
            </tr>
          ))}
        </tbody>
      </ResizableTable>
    </div>
  );
}
