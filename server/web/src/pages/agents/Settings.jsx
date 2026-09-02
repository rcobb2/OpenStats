import { useState, useEffect } from 'react';
import { getSettings, updateSettings, getRolloutStatus } from '../../api';

export default function Settings() {
  const [settings, setSettings] = useState({
    heartbeatIntervalSeconds: 120,
    updateIntervalSeconds: 3600,
    staleTimeoutDays: 90,
    minAgentVersion: '0.1.0',
    maintenanceWindowStart: '',
    maintenanceWindowEnd: '',
    autoUpdateEnabled: false,
    rolloutMaxConcurrent: 20,
    rolloutGraceSeconds: 900,
    targetAgentVersion: '',
  });
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState(null);
  const [error, setError] = useState(null);
  const [rollout, setRollout] = useState(null);

  useEffect(() => {
    fetchSettings();
    getRolloutStatus().then(setRollout).catch(() => setRollout(false));
  }, []);

  const fetchSettings = async () => {
    try {
      const data = await getSettings();
      setSettings(data);
    } catch (err) {
      setError("Failed to load settings: " + err.message);
    } finally {
      setLoading(false);
    }
  };

  const handleSave = async (e) => {
    e.preventDefault();
    setSaving(true);
    setMessage(null);
    setError(null);
    try {
      await updateSettings(settings);
      setMessage("Settings saved successfully. Agents will pick up changes on their next heartbeat.");
    } catch (err) {
      setError("Failed to save settings: " + err.message);
    } finally {
      setSaving(false);
    }
  };

  if (loading) return <div>Loading settings...</div>;

  return (
    <div className="settings-page">
      <h2>Agent & Fleet Settings</h2>
      <p>Global configuration pushed to all agents during registration/heartbeat.</p>

      {message && <div className="success-banner">{message}</div>}
      {error && <div className="error-banner">{error}</div>}

      <form onSubmit={handleSave} className="form-stack card">
        <section>
          <h3>Communication</h3>
          <label>
            Heartbeat Interval (seconds)
            <p className="hint">How often agents check in and report status.</p>
            <input 
              type="number" 
              value={settings.heartbeatIntervalSeconds} 
              onChange={e => setSettings({...settings, heartbeatIntervalSeconds: parseInt(e.target.value) || 120})} 
            />
          </label>
        </section>

        <section>
          <h3>Updates & Scans</h3>
          <label>
            Update Check Interval (seconds)
            <p className="hint">How often agents pull new software mappings and scan inventory (default 1h).</p>
            <input 
              type="number" 
              value={settings.updateIntervalSeconds} 
              onChange={e => setSettings({...settings, updateIntervalSeconds: parseInt(e.target.value) || 3600})} 
            />
          </label>
          <label>
            Minimum Required Agent Version
            <p className="hint">Agents below this version are flagged "out of date" in the fleet list. This is just a display flag — automatic rollout is controlled separately below.</p>
            <input
              type="text"
              value={settings.minAgentVersion}
              onChange={e => setSettings({...settings, minAgentVersion: e.target.value})}
            />
          </label>
        </section>

        <section>
          <h3>Automatic Updates</h3>
          <label style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', flexDirection: 'row' }}>
            <input
              type="checkbox"
              checked={!!settings.autoUpdateEnabled}
              onChange={e => setSettings({...settings, autoUpdateEnabled: e.target.checked})}
              style={{ width: 'auto' }}
            />
            Enable staggered auto-update rollout
          </label>
          <p className="hint">
            When on, the server gradually rolls agents forward to the newest published
            installer (macOS and Windows tracked independently), a few at a time, only
            during the maintenance window below. Leave off to freeze the fleet.
          </p>
          <label>
            Max Concurrent Updates
            <p className="hint">How many agents may be installing at once (0 = unlimited). Keep low to avoid overloading the server with simultaneous downloads.</p>
            <input
              type="number"
              value={settings.rolloutMaxConcurrent ?? 20}
              onChange={e => setSettings({...settings, rolloutMaxConcurrent: parseInt(e.target.value) || 0})}
            />
          </label>
          <label>
            Target Version Pin (optional)
            <p className="hint">Leave blank to auto-track the newest installer. Set to a specific version to pin/pause a rollout (agents above it are left alone).</p>
            <input
              type="text"
              placeholder="(auto — newest installer)"
              value={settings.targetAgentVersion || ''}
              onChange={e => setSettings({...settings, targetAgentVersion: e.target.value})}
            />
          </label>
          <RolloutStatusCard rollout={rollout} />
        </section>

        <section>
          <h3>Fleet Maintenance</h3>
          <p className="hint">Auto-update rollouts only run inside this window. Leave both blank to allow updates at any time.</p>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '1rem' }}>
            <label>
              Maintenance Window Start
              <input
                type="time"
                value={settings.maintenanceWindowStart || ''}
                onChange={e => setSettings({...settings, maintenanceWindowStart: e.target.value})}
              />
            </label>
            <label>
              Maintenance Window End
              <input
                type="time"
                value={settings.maintenanceWindowEnd || ''}
                onChange={e => setSettings({...settings, maintenanceWindowEnd: e.target.value})}
              />
            </label>
          </div>
          <label style={{ marginTop: '1rem' }}>
            Stale Agent Timeout (days)
            <p className="hint">Automatically remove agents from the database if they haven't checked in for this long.</p>
            <input 
              type="number" 
              value={settings.staleTimeoutDays} 
              onChange={e => setSettings({...settings, staleTimeoutDays: parseInt(e.target.value) || 90})} 
            />
          </label>
        </section>

        <div className="form-actions">
          <button type="submit" className="primary" disabled={saving}>
            {saving ? 'Saving...' : 'Save Settings'}
          </button>
        </div>
      </form>
    </div>
  );
}

// RolloutStatusCard shows live per-platform update progress from
// GET /agents/rollout-status so an operator can watch a rollout drain.
function RolloutStatusCard({ rollout }) {
  if (rollout === null) return <p className="hint">Loading rollout status…</p>;
  if (rollout === false) return <p className="hint">Rollout status unavailable.</p>;
  const platforms = rollout.platforms || [];
  if (platforms.length === 0) return <p className="hint">No agents enrolled yet.</p>;

  const barStyle = { height: 8, borderRadius: 4, background: 'var(--border)', overflow: 'hidden', display: 'flex' };
  return (
    <div style={{ marginTop: '0.75rem', border: '1px solid var(--border)', borderRadius: 8, padding: '0.9rem' }}>
      <div style={{ fontSize: 13, color: 'var(--text-dim)', marginBottom: '0.6rem' }}>
        Rollout progress — {rollout.inFlightGlobal} installing now
        {rollout.maxConcurrent > 0 ? ` (max ${rollout.maxConcurrent})` : ' (unlimited)'}
      </div>
      {platforms.map(p => {
        const total = p.total || 1;
        const pct = n => `${Math.round((n / total) * 100)}%`;
        return (
          <div key={p.platform} style={{ marginBottom: '0.7rem' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 13, marginBottom: 3 }}>
              <span>{p.platform} → <strong>{p.target || '—'}</strong></span>
              <span style={{ color: 'var(--text-dim)' }}>
                {p.updated}/{p.total} updated{p.updating ? `, ${p.updating} updating` : ''}{p.pending ? `, ${p.pending} pending` : ''}
              </span>
            </div>
            <div style={barStyle}>
              <div style={{ width: pct(p.updated), background: '#43b581' }} title={`updated: ${p.updated}`} />
              <div style={{ width: pct(p.updating), background: '#f0a030' }} title={`updating: ${p.updating}`} />
              <div style={{ width: pct(p.pending), background: 'var(--border)' }} title={`pending: ${p.pending}`} />
            </div>
          </div>
        );
      })}
    </div>
  );
}
