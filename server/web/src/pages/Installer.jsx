import { useState } from 'react';
import { generateInstaller } from '../api';

export default function Installer() {
  const [form, setForm] = useState({ serverAddress: '', port: 9183 });
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

  return (
    <div>
      <h2>Installer Generator</h2>
      <p>Generate a pre-configured MSI install command for deploying agents to lab machines.</p>

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
        </div>
      )}
    </div>
  );
}
