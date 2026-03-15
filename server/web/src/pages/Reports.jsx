import { useState, useEffect } from 'react';
import { getTopApps, getActiveUsers } from '../api';
import ResizableTable from '../components/Table';

export default function Reports() {
  const [range, setRange] = useState('24h');
  const [topApps, setTopApps] = useState(null);
  const [activeUsers, setActiveUsers] = useState(null);

  useEffect(() => {
    getTopApps(range).then(setTopApps).catch(() => {});
    getActiveUsers().then(setActiveUsers).catch(() => {});
  }, [range]);

  const parsePromResult = (data) => {
    if (!data?.data?.result) return [];
    return data.data.result.map(r => ({
      labels: r.metric || {},
      value: r.value ? parseFloat(r.value[1]) : 0,
    })).filter(r => r.value > 0).sort((a, b) => b.value - a.value);
  };

  const apps = topApps ? parsePromResult(topApps) : [];
  const users = activeUsers ? parsePromResult(activeUsers) : [];

  return (
    <div>
      <h2>Reports</h2>

      <div className="controls">
        <label>Time Range: </label>
        <select value={range} onChange={e => setRange(e.target.value)}>
          <option value="1h">Last Hour</option>
          <option value="24h">Last 24 Hours</option>
          <option value="7d">Last 7 Days</option>
          <option value="30d">Last 30 Days</option>
        </select>
      </div>

      <h3>Top Applications by Usage ({range})</h3>
      {apps.length > 0 ? (
        <ResizableTable>
          <thead><tr><th>Application</th><th>Category</th><th>Usage (hours)</th></tr></thead>
          <tbody>
            {apps.slice(0, 20).map((a, i) => (
              <tr key={i}>
                <td>{a.labels.app || 'Unknown'}</td>
                <td>{a.labels.category || ''}</td>
                <td>{(a.value / 3600).toFixed(1)}</td>
              </tr>
            ))}
          </tbody>
        </ResizableTable>
      ) : (
        <p className="empty">No usage data available for this time range. Ensure Prometheus is running and agents are reporting.</p>
      )}

      <h3>Active Users</h3>
      {users.length > 0 ? (
        <ResizableTable>
          <thead><tr><th>User</th><th>Hostname</th></tr></thead>
          <tbody>
            {users.map((u, i) => (
              <tr key={i}>
                <td>{u.labels.user || 'Unknown'}</td>
                <td>{u.labels.hostname || ''}</td>
              </tr>
            ))}
          </tbody>
        </ResizableTable>
      ) : (
        <p className="empty">No active user sessions.</p>
      )}
    </div>
  );
}
