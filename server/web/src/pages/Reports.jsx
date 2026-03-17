import { useState, useEffect } from 'react';
import { 
  exportTopAppsByLaunches, 
  exportTopAppsByForeground,
  exportBottomAppsByLaunches,
  exportBottomAppsByForeground
} from '../api';

export default function Reports() {
  const [range, setRange] = useState('24h');
  const [reportType, setReportType] = useState('user');
  const [exporting, setExporting] = useState(false);

  const reports = {
    user: { title: 'User Behavior Analytics', uid: 'ols-users', slug: 'user-behavior-analytics' },
    hardware: { title: 'Hardware & Asset Utilization', uid: 'ols-hardware', slug: 'hardware-asset-utilization' },
    software: { title: 'Software Metering & License Compliance', uid: 'software-metering', slug: 'software-metering' },
  };

  const rangeMap = {
    '1h': 'now-1h',
    '24h': 'now-24h',
    '7d': 'now-7d',
    '30d': 'now-30d',
  };

  const grafanaSrc = `/grafana/d/${reports[reportType].uid}/${reports[reportType].slug}?orgId=1&refresh=30s&kiosk&theme=dark&from=${rangeMap[range]}&to=now`;

  const handleExport = async (exportFn) => {
    setExporting(true);
    try {
      await exportFn(range);
    } catch (err) {
      alert('Export failed: ' + err.message);
    } finally {
      setExporting(false);
    }
  };

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: '1rem' }}>
        <h2>{reports[reportType].title}</h2>
        <div className="controls" style={{ margin: 0, display: 'flex', gap: '1rem', flexWrap: 'wrap' }}>
          <div>
            <label>Report: </label>
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
          {reportType === 'software' && (
            <div style={{ display: 'flex', gap: '0.5rem' }}>
              <button 
                className="btn-secondary" 
                onClick={() => handleExport(exportTopAppsByLaunches)}
                disabled={exporting}
                title="Export Top 10 by Launch Count"
              >
                CSV: Top 10 Launches
              </button>
              <button 
                className="btn-secondary" 
                onClick={() => handleExport(exportTopAppsByForeground)}
                disabled={exporting}
                title="Export Top 10 by Active Time"
              >
                CSV: Top 10 Active Time
              </button>
              <button 
                className="btn-secondary" 
                onClick={() => handleExport(exportBottomAppsByLaunches)}
                disabled={exporting}
                title="Export Bottom 10 by Launch Count"
              >
                CSV: Bottom 10 Launches
              </button>
              <button 
                className="btn-secondary" 
                onClick={() => handleExport(exportBottomAppsByForeground)}
                disabled={exporting}
                title="Export Bottom 10 by Active Time"
              >
                CSV: Bottom 10 Active Time
              </button>
            </div>
          )}
        </div>
      </div>

      <div style={{ 
        background: 'var(--card-bg)', 
        borderRadius: '8px', 
        border: '1px solid var(--border)',
        overflow: 'hidden',
        height: 'calc(100vh - 220px)',
        minHeight: '500px',
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
    </div>
  );
}
