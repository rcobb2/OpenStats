import { useState, useEffect } from 'react';
import { getTopApps, getActiveUsers } from '../api';
import ResizableTable from '../components/Table';

export default function Reports() {
  const [range, setRange] = useState('24h');
  const [reportType, setReportType] = useState('user'); // 'user', 'hardware', 'software'
  const [topApps, setTopApps] = useState(null);
  const [activeUsers, setActiveUsers] = useState(null);

  // Map our UI ranges to Grafana time ranges
  const rangeMap = {
    '1h': 'now-1h',
    '24h': 'now-24h',
    '7d': 'now-7d',
    '30d': 'now-30d',
  };

  const reports = {
    user: { title: 'User Behavior Analytics', uid: 'ols-users', slug: 'user-behavior-analytics' },
    hardware: { title: 'Hardware & Asset Utilization', uid: 'ols-hardware', slug: 'hardware-asset-utilization' },
    software: { title: 'Software Metering & License Compliance', uid: 'ols-software', slug: 'software-metering-license-compliance' },
  };

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

  const grafanaSrc = `/grafana/d/${reports[reportType].uid}/${reports[reportType].slug}?orgId=1&refresh=30s&kiosk&theme=dark&from=${rangeMap[range]}&to=now`;

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <h2>{reports[reportType].title}</h2>
        <div className="controls" style={{ margin: 0, display: 'flex', gap: '1rem' }}>
          <div>
            <label>Report Type: </label>
            <select value={reportType} onChange={e => setReportType(e.target.value)}>
              <option value="user">User Behavior</option>
              <option value="hardware">Hardware Utilization</option>
              <option value="software">Software Metering</option>
            </select>
          </div>
          <div>
            <label>Time Range: </label>
            <select value={range} onChange={e => setRange(e.target.value)}>
              <option value="1h">Last Hour</option>
              <option value="24h">Last 24 Hours</option>
              <option value="7d">Last 7 Days</option>
              <option value="30d">Last 30 Days</option>
            </select>
          </div>
        </div>
      </div>

      <div style={{ 
        background: 'var(--card-bg)', 
        borderRadius: '8px', 
        border: '1px solid var(--border)',
        overflow: 'hidden',
        height: '600px',
        marginBottom: '2rem',
        marginTop: '1rem'
      }}>
        <iframe 
          src={grafanaSrc} 
          width="100%" 
          height="100%" 
          frameBorder="0" 
          title="Grafana Dashboard"
          style={{ display: 'block' }}
        />
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '2rem' }}>
        <section>
          <h3>Top Applications ({range})</h3>
          {apps.length > 0 ? (
            <ResizableTable>
              <thead><tr><th>Application</th><th>Usage (h)</th></tr></thead>
              <tbody>
                {apps.slice(0, 10).map((a, i) => (
                  <tr key={i}>
                    <td>{a.labels.app || 'Unknown'}</td>
                    <td>{(a.value / 3600).toFixed(1)}</td>
                  </tr>
                ))}
              </tbody>
            </ResizableTable>
          ) : (
            <p className="empty">No usage data.</p>
          )}
        </section>

        <section>
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
            <p className="empty">No active users.</p>
          )}
        </section>
      </div>
    </div>
  );
}
