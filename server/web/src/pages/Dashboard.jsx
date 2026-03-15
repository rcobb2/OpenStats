import { useState, useEffect } from 'react';
import { getSummary } from '../api';

export default function Dashboard() {
  const [summary, setSummary] = useState(null);
  const [error, setError] = useState(null);

  useEffect(() => {
    getSummary().then(setSummary).catch(e => setError(e.message));
  }, []);

  if (error) return <div className="error">Failed to load: {error}</div>;
  if (!summary) return <div className="loading">Loading...</div>;

  return (
    <div>
      <h2>Dashboard</h2>
      <div className="stats-grid">
        <div className="stat-card">
          <span className="stat-value">{summary.totalAgents}</span>
          <span className="stat-label">Total Agents</span>
        </div>
        <div className="stat-card">
          <span className="stat-value">{summary.onlineAgents}</span>
          <span className="stat-label">Online</span>
        </div>
        <div className="stat-card">
          <span className="stat-value">{summary.totalLabs}</span>
          <span className="stat-label">Labs</span>
        </div>
        <div className="stat-card">
          <span className="stat-value">{summary.totalMappings}</span>
          <span className="stat-label">Mappings</span>
        </div>
      </div>
    </div>
  );
}
