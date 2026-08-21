import { useState, useEffect, useCallback } from 'react';
import {
  getUsers, getUserRules, saveUserRule, deleteUserRule, patchUserRuleIgnore,
  ignoreUser, getUserPolicy, updateUserPolicy,
} from '../api';
import ResizableTable from '../components/Table';

const EMPTY_RULE = { pattern: '', canonicalUser: '', displayName: '', notes: '', ignored: false };

export default function Users() {
  const [users, setUsers] = useState([]);
  const [rules, setRules] = useState([]);
  const [policy, setPolicy] = useState(null);
  const [view, setView] = useState('discovered');
  const [filter, setFilter] = useState('');
  const [form, setForm] = useState(EMPTY_RULE);
  const [showAddForm, setShowAddForm] = useState(false);
  const [mergeFor, setMergeFor] = useState(null);
  const [mergeTarget, setMergeTarget] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [discovered, ruleList, pol] = await Promise.all([
        getUsers().catch(() => []),
        getUserRules(),
        getUserPolicy(),
      ]);
      setUsers(discovered || []);
      setRules(ruleList || []);
      setPolicy(pol);
    } catch {
      setError('Failed to load users.');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  const ignoredCount = users.filter(u => u.ignored).length;
  const trackedUsers = users.filter(u => !u.ignored);
  const mergedUsers = users.filter(u => u.rawUsers.length > 1);

  const matches = (text) => !filter || (text || '').toLowerCase().includes(filter.toLowerCase());

  const visibleUsers = users.filter(u => {
    if (view === 'tracked' && u.ignored) return false;
    if (view === 'ignored' && !u.ignored) return false;
    if (view === 'merged' && u.rawUsers.length < 2) return false;
    return matches(u.canonicalUser) || u.rawUsers.some(matches);
  });

  const visibleRules = rules.filter(r => matches(r.pattern) || matches(r.canonicalUser));

  const run = async (fn, failure) => {
    setError(''); setSaving(true);
    try {
      await fn();
      await load();
    } catch (e) {
      setError(e?.message || failure);
    } finally {
      setSaving(false);
    }
  };

  const handleAddRule = (e) => {
    e.preventDefault();
    if (!form.pattern) return;
    run(async () => {
      await saveUserRule(form);
      setForm(EMPTY_RULE);
      setShowAddForm(false);
    }, 'Failed to save rule.');
  };

  const handleIgnoreUser = (u) => {
    const target = u.rawUsers.length === 1 ? u.rawUsers[0] : u.canonicalUser;
    run(() => ignoreUser(target, 'Ignored from Users page'), 'Failed to ignore user.');
  };

  const handleUnignoreUser = (u) => {
    // Ignoring can come from a rule on any of the raw names or on the canonical
    // one; clear every matching ignore rule so the user really comes back.
    const names = new Set([u.canonicalUser, ...u.rawUsers].map(n => n.toLowerCase()));
    const toClear = rules.filter(r => r.ignored && names.has(r.pattern.toLowerCase()));
    if (toClear.length === 0) {
      setError(`${u.canonicalUser} is excluded by a built-in rule, not an editable one. Add a rule with a canonical name to track it.`);
      return;
    }
    run(() => Promise.all(toClear.map(r => patchUserRuleIgnore(r.id, false))), 'Failed to unignore user.');
  };

  const handleMerge = (e) => {
    e.preventDefault();
    if (!mergeFor || !mergeTarget) return;
    run(async () => {
      await saveUserRule({
        ...EMPTY_RULE,
        pattern: mergeFor.canonicalUser,
        canonicalUser: mergeTarget,
        notes: `Merged from ${mergeFor.rawUsers.join(', ')}`,
      });
      setMergeFor(null);
      setMergeTarget('');
    }, 'Failed to merge users.');
  };

  const handleDeleteRule = (id) => {
    if (!confirm('Delete this rule?')) return;
    run(() => deleteUserRule(id), 'Failed to delete rule.');
  };

  const handleToggleStripDomain = () => {
    const next = !policy.stripDomain;
    if (!next && !confirm('Turn off domain stripping? COLGATE\\jdoe and jdoe will be counted as two different users.')) return;
    run(() => updateUserPolicy({ stripDomain: next }), 'Failed to update policy.');
  };

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', gap: '1rem', marginBottom: '1rem' }}>
        <h2 style={{ margin: 0 }}>Users</h2>
        <button
          onClick={() => { setShowAddForm(v => !v); setError(''); }}
          style={{ marginLeft: 'auto', padding: '0.35rem 0.85rem', cursor: 'pointer' }}
        >
          {showAddForm ? 'Cancel' : '+ Add Rule'}
        </button>
      </div>

      {error && <div style={{ color: 'var(--error, #e55353)', marginBottom: '1rem' }}>{error}</div>}

      {policy && (
        <div style={{ padding: '0.6rem 0.85rem', marginBottom: '1rem', borderRadius: '4px', background: 'var(--surface2, rgba(255,255,255,0.05))', fontSize: '0.875rem' }}>
          <label style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', cursor: 'pointer' }}>
            <input type="checkbox" checked={policy.stripDomain} onChange={handleToggleStripDomain} disabled={saving} />
            <span>
              Correlate identities across platforms — treat <code>COLGATE\jdoe</code>,{' '}
              <code>jdoe@colgate.edu</code>, and <code>jdoe</code> as the same user.
            </span>
          </label>
        </div>
      )}

      {showAddForm && (
        <form onSubmit={handleAddRule} className="form-inline" style={{ marginBottom: '1.25rem' }}>
          <input placeholder="Username or pattern (e.g. zabbix, svc-*)" value={form.pattern}
            onChange={e => setForm({ ...form, pattern: e.target.value })} required />
          <input placeholder="Merge into (leave blank to ignore)" value={form.canonicalUser}
            onChange={e => setForm({ ...form, canonicalUser: e.target.value, ignored: e.target.value ? false : form.ignored })} />
          <input placeholder="Display name (optional)" value={form.displayName}
            onChange={e => setForm({ ...form, displayName: e.target.value })} />
          <input placeholder="Notes (optional)" value={form.notes}
            onChange={e => setForm({ ...form, notes: e.target.value })} />
          <label style={{ display: 'flex', alignItems: 'center', gap: '0.35rem' }}>
            <input type="checkbox" checked={form.ignored} disabled={!!form.canonicalUser}
              onChange={e => setForm({ ...form, ignored: e.target.checked })} />
            Ignore
          </label>
          <button type="submit" disabled={saving || (!form.ignored && !form.canonicalUser)}>Save</button>
        </form>
      )}

      <div style={{ display: 'flex', gap: '0.5rem', marginBottom: '0.75rem', alignItems: 'center', flexWrap: 'wrap' }}>
        <TabButton active={view === 'discovered'} onClick={() => setView('discovered')}>
          All <span className="badge">{users.length}</span>
        </TabButton>
        <TabButton active={view === 'tracked'} onClick={() => setView('tracked')}>
          Tracked <span className="badge" style={{ marginLeft: '0.4em' }}>{trackedUsers.length}</span>
        </TabButton>
        <TabButton active={view === 'ignored'} onClick={() => setView('ignored')}>
          Ignored
          {ignoredCount > 0 && <span className="badge" style={{ marginLeft: '0.4em' }}>{ignoredCount}</span>}
        </TabButton>
        <TabButton active={view === 'merged'} onClick={() => setView('merged')}>
          Merged
          {mergedUsers.length > 0 && <span className="badge" style={{ marginLeft: '0.4em' }}>{mergedUsers.length}</span>}
        </TabButton>
        <TabButton active={view === 'rules'} onClick={() => setView('rules')}>
          Rules <span className="badge" style={{ marginLeft: '0.4em' }}>{rules.length}</span>
        </TabButton>
        <input
          className="search"
          placeholder="Filter..."
          value={filter}
          onChange={e => setFilter(e.target.value)}
          style={{ marginLeft: 'auto', width: '200px' }}
        />
      </div>

      {mergeFor && (
        <form onSubmit={handleMerge} className="form-inline" style={{ marginBottom: '1rem' }}>
          <span>
            Merge <code>{mergeFor.canonicalUser}</code> into:
          </span>
          <input
            placeholder="Canonical username (e.g. jdoe)"
            value={mergeTarget}
            onChange={e => setMergeTarget(e.target.value)}
            autoFocus
            required
          />
          <button type="submit" disabled={saving}>Merge</button>
          <button type="button" onClick={() => { setMergeFor(null); setMergeTarget(''); }}>Cancel</button>
        </form>
      )}

      {loading ? (
        <div style={{ color: 'var(--text-muted, #aaa)' }}>Loading…</div>
      ) : view === 'rules' ? (
        <RulesTable
          rules={visibleRules}
          filter={filter}
          saving={saving}
          onToggleIgnore={(r) => run(() => patchUserRuleIgnore(r.id, !r.ignored), 'Failed to update rule.')}
          onDelete={handleDeleteRule}
        />
      ) : (
        <DiscoveredTable
          users={visibleUsers}
          filter={filter}
          saving={saving}
          onIgnore={handleIgnoreUser}
          onUnignore={handleUnignoreUser}
          onMerge={(u) => { setMergeFor(u); setMergeTarget(''); setError(''); }}
        />
      )}
    </div>
  );
}

function DiscoveredTable({ users, filter, saving, onIgnore, onUnignore, onMerge }) {
  return (
    <ResizableTable>
      <thead>
        <tr>
          <th>User</th>
          <th>Seen As</th>
          <th>Session Hours (30d)</th>
          <th>Status</th>
          <th>Actions</th>
        </tr>
      </thead>
      <tbody>
        {users.map(u => (
          <tr key={u.canonicalUser} style={u.ignored ? { opacity: 0.45 } : undefined}>
            <td>
              <code>{u.canonicalUser}</code>
              {u.displayName && <div style={{ fontSize: '0.8rem', color: 'var(--text-muted, #aaa)' }}>{u.displayName}</div>}
            </td>
            <td>
              {u.rawUsers.map(raw => (
                <div key={raw} style={{ fontSize: '0.85rem' }}><code>{raw}</code></div>
              ))}
            </td>
            <td>{u.sessionHours ? u.sessionHours.toFixed(1) : '—'}</td>
            <td>
              {u.ignored ? (
                <span className="badge">ignored</span>
              ) : u.activeNow ? (
                <span className="badge" style={{ background: '#2ecc71', color: '#fff' }}>active</span>
              ) : (
                <span className="badge">tracked</span>
              )}
            </td>
            <td>
              <div style={{ display: 'flex', gap: '0.4rem', flexWrap: 'wrap' }}>
                {u.ignored ? (
                  <button onClick={() => onUnignore(u)} disabled={saving}>Unignore</button>
                ) : (
                  <>
                    <button onClick={() => onMerge(u)} disabled={saving} title="Merge into another username">Merge</button>
                    <button onClick={() => onIgnore(u)} disabled={saving} title="Ignore — drop from metrics and reports">Ignore</button>
                  </>
                )}
              </div>
            </td>
          </tr>
        ))}
        {users.length === 0 && (
          <tr><td colSpan={5} style={{ textAlign: 'center', color: 'var(--text-muted, #aaa)', padding: '1.5rem' }}>
            {filter ? 'No users match the filter.' : 'No users recorded yet.'}
          </td></tr>
        )}
      </tbody>
    </ResizableTable>
  );
}

function RulesTable({ rules, filter, saving, onToggleIgnore, onDelete }) {
  return (
    <ResizableTable>
      <thead>
        <tr>
          <th>Pattern</th>
          <th>Merges Into</th>
          <th>Notes</th>
          <th>Source</th>
          <th>Actions</th>
        </tr>
      </thead>
      <tbody>
        {rules.map(r => (
          <tr key={r.id}>
            <td><code>{r.pattern}</code></td>
            <td>{r.ignored ? <span className="badge">ignored</span> : <code>{r.canonicalUser}</code>}</td>
            <td style={{ fontSize: '0.85rem', color: 'var(--text-muted, #aaa)' }}>{r.notes}</td>
            <td><span className="badge">{r.source}</span></td>
            <td>
              <div style={{ display: 'flex', gap: '0.4rem', flexWrap: 'wrap' }}>
                {r.canonicalUser === '' && (
                  <button onClick={() => onToggleIgnore(r)} disabled={saving}>
                    {r.ignored ? 'Unignore' : 'Ignore'}
                  </button>
                )}
                <button className="btn-danger" onClick={() => onDelete(r.id)} disabled={saving}>Delete</button>
              </div>
            </td>
          </tr>
        ))}
        {rules.length === 0 && (
          <tr><td colSpan={5} style={{ textAlign: 'center', color: 'var(--text-muted, #aaa)', padding: '1.5rem' }}>
            {filter ? 'No rules match the filter.' : 'No rules yet. System and service accounts are filtered by built-in defaults.'}
          </td></tr>
        )}
      </tbody>
    </ResizableTable>
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
