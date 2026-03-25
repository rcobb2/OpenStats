import { useState } from 'react';
import { generateInstaller, getMacInstallerURL } from '../api';

export default function Installer() {
  const [tab, setTab] = useState('windows');
  const [form, setForm] = useState({ serverAddress: '', port: 9183, building: '', room: '' });
  const [result, setResult] = useState(null);
  const [error, setError] = useState(null);

  const handleSubmit = async (e) => {
    e.preventDefault();
    setError(null);
    setResult(null);
    try {
      const res = await generateInstaller(form);
      setResult(res);
    } catch (err) {
      setError(err.message);
    }
  };

  const macDownloadURL = getMacInstallerURL();

  return (
    <div>
      <h2>Installer Generator</h2>

      <div className="tab-bar">
        <button
          className={tab === 'windows' ? 'tab active' : 'tab'}
          onClick={() => setTab('windows')}
        >
          Windows
        </button>
        <button
          className={tab === 'mac' ? 'tab active' : 'tab'}
          onClick={() => setTab('mac')}
        >
          macOS
        </button>
      </div>

      {tab === 'windows' && (
        <div>
          <p>Generate a pre-configured MSI install command for deploying agents to Windows lab machines.</p>
          <form onSubmit={handleSubmit} className="form-stack">
            <label>
              Server Address
              <input type="text" placeholder="server.campus.edu" value={form.serverAddress}
                onChange={e => setForm({ ...form, serverAddress: e.target.value })} required />
            </label>
            <label>
              Metrics Port
              <input type="number" value={form.port}
                onChange={e => setForm({ ...form, port: parseInt(e.target.value) || 9183 })} />
            </label>
            <label>
              Building (optional)
              <input type="text" placeholder="Science Hall" value={form.building}
                onChange={e => setForm({ ...form, building: e.target.value })} />
            </label>
            <label>
              Room (optional)
              <input type="text" placeholder="302" value={form.room}
                onChange={e => setForm({ ...form, room: e.target.value })} />
            </label>
            <button type="submit">Generate</button>
          </form>

          {error && <div className="error">{error}</div>}

          {result && (
            <div className="result-box">
              <h3>Install Command</h3>
              <p>Run this on target machines (as Administrator):</p>
              <pre><code>{result.installCommand}</code></pre>
              <h3>Silent Deployment (SCCM / Intune / GPO)</h3>
              <pre><code>{result.installCommand} /l*v C:\temp\openlabstats-install.log</code></pre>
              {result.downloadUrl && (
                <p><a href={result.downloadUrl} download>Download MSI</a></p>
              )}
            </div>
          )}
        </div>
      )}

      {tab === 'mac' && (
        <div>
          <p>Deploy the macOS agent as a LaunchDaemon on Apple Silicon or Intel lab machines.</p>

          <div className="result-box">
            <h3>1. Download the Package</h3>
            <p><a href={macDownloadURL} download>Download latest macOS .pkg</a></p>

            <h3>2. Install</h3>
            <p>Run as root on the target machine:</p>
            <pre><code>sudo installer -pkg openlabstats-agent-*.pkg -target /</code></pre>

            <h3>3. Configure Building &amp; Room</h3>
            <p>Edit the config file to set the location for this machine:</p>
            <pre><code>sudo nano /usr/local/openlabstats/configs/agent.yaml</code></pre>
            <p>Set the <code>building</code> and <code>room</code> fields under <code>monitor:</code>, then restart the agent:</p>
            <pre><code>sudo launchctl bootout system/com.openlabstats.agent
sudo launchctl bootstrap system /Library/LaunchDaemons/com.openlabstats.agent.plist</code></pre>

            <h3>Self-Update</h3>
            <p>
              Once enrolled, agents update automatically. When you upload a new .pkg to the server and
              set the minimum agent version in <strong>Settings</strong>, outdated agents will download
              and install the update on their next heartbeat — no manual intervention needed.
              You can also trigger an immediate update from the <strong>Agents</strong> page.
            </p>
          </div>
        </div>
      )}
    </div>
  );
}
