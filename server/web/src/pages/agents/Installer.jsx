import { useState } from 'react';
import { generateInstaller } from '../../api';

export default function Installer() {
  const [form, setForm] = useState({ serverAddress: '', port: 9183, building: '', room: '' });
  const [result, setResult] = useState(null);
  const [error, setError] = useState(null);

  const handleSubmit = async (e) => {
    e.preventDefault();
    setError(null);
    setResult(null);
    try {
      // In a real app, generateInstaller might return a URL or a formatted command.
      // Our backend current GenerateInstaller doesn't yet support building/room as inputs for the command string generation,
      // but we can pass them anyway if we update the backend later.
      const res = await generateInstaller(form);
      
      // Manually enhance the result if it doesn't include the new switches
      let cmd = res.installCommand;
      if (form.building) cmd += ` BUILDING="${form.building}"`;
      if (form.room) cmd += ` ROOM="${form.room}"`;
      
      setResult({ ...res, installCommand: cmd });
    } catch (err) {
      setError(err.message);
    }
  };

  return (
    <div>
      <h2>Installer Generator</h2>
      <p>Generate a pre-configured MSI install command for deploying agents to lab machines.</p>

      <form onSubmit={handleSubmit} className="form-stack">
        <label>
          Server Address
          <input type="text" placeholder="http://server.campus.edu:8080" value={form.serverAddress}
            onChange={e => setForm({ ...form, serverAddress: e.target.value })} required />
        </label>
        <label>
          Metrics Port
          <input type="number" value={form.port}
            onChange={e => setForm({ ...form, port: parseInt(e.target.value) || 9183 })} />
        </label>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '1rem' }}>
          <label>
            Default Building (Optional)
            <input type="text" placeholder="Science Hall" value={form.building}
              onChange={e => setForm({ ...form, building: e.target.value })} />
          </label>
          <label>
            Default Room (Optional)
            <input type="text" placeholder="302" value={form.room}
              onChange={e => setForm({ ...form, room: e.target.value })} />
          </label>
        </div>
        <button type="submit">Generate</button>
      </form>

      {error && <div className="error">{error}</div>}

      {result && (
        <div className="result-box">
          <h3>Install Command</h3>
          <p>Run this on target machines (as Administrator):</p>
          <pre style={{ whiteSpace: 'pre-wrap' }}><code>{result.installCommand} /qn</code></pre>
          <h3>Silent Deployment (SCCM / Intune / GPO)</h3>
          <pre style={{ whiteSpace: 'pre-wrap' }}><code>{result.installCommand} /qn /l*v C:\temp\openlabstats-install.log</code></pre>
        </div>
      )}
    </div>
  );
}
