import { useState, useEffect } from 'react';
import { getSettings, updateSettings } from '../../api';

export default function Settings() {
  const [settings, setSettings] = useState({
    heartbeatIntervalSeconds: 120,
    updateIntervalSeconds: 3600,
    staleTimeoutDays: 90,
    minAgentVersion: '0.1.0'
  });
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState(null);
  const [error, setError] = useState(null);

  useEffect(() => {
    fetchSettings();
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
            <p className="hint">Agents below this version will be flagged as out of date.</p>
            <input 
              type="text" 
              value={settings.minAgentVersion} 
              onChange={e => setSettings({...settings, minAgentVersion: e.target.value})} 
            />
          </label>
        </section>

        <section>
          <h3>Fleet Maintenance</h3>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '1rem' }}>
            <label>
              Maintenance Window Start
              <input 
                type="time" 
                value={settings.maintenanceWindowStart} 
                onChange={e => setSettings({...settings, maintenanceWindowStart: e.target.value})} 
              />
            </label>
            <label>
              Maintenance Window End
              <input 
                type="time" 
                value={settings.maintenanceWindowEnd} 
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
