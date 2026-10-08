import { useState, useEffect } from 'react';
import {
  getTopAppsByForeground, getTopAppsByLaunches,
  getTopUsersByLogins, getTopAppsByElevations, parsePromVector,
} from '../api';
import MiniBarList from './MiniBarList';

// The four-panel "what's this one entity been doing" grid — identical logic
// used to be hand-duplicated in both AgentDetail.jsx and LabDetail.jsx (same
// four fetches, same four useState pairs, same panel-grid JSX), differing
// only in the filter object passed to each query ({hostname} vs {lab}). That
// duplication was a real cost, not just untidy: today's Dashboard fix
// (replacing a chronically-empty Elevations panel with Top Users by Session
// Time) only touched Dashboard.jsx, leaving these two copies' panel choices
// silently inconsistent with it. One component now backs both pages, so a
// future panel-content change is a single edit instead of two kept in sync
// by hand.
export default function EntityActivityPanels({ filters, range, ready }) {
  const [foreground, setForeground] = useState(null);
  const [foregroundError, setForegroundError] = useState(false);
  const [launches, setLaunches] = useState(null);
  const [launchesError, setLaunchesError] = useState(false);
  const [logins, setLogins] = useState(null);
  const [loginsError, setLoginsError] = useState(false);
  const [elevations, setElevations] = useState(null);
  const [elevationsError, setElevationsError] = useState(false);

  // filters is a plain object literal at every call site (e.g. {hostname: id}
  // or {lab: lab.name}) — stringify it for the effect dependency so a new
  // object with the same contents doesn't re-trigger these fetches on every
  // parent render.
  const filterKey = JSON.stringify(filters || {});

  useEffect(() => {
    if (!ready) return;

    setForeground(null); setForegroundError(false);
    getTopAppsByForeground(range, 10, filters)
      .then(r => setForeground(parsePromVector(r))).catch(() => setForegroundError(true));

    setLaunches(null); setLaunchesError(false);
    getTopAppsByLaunches(range, 10, filters)
      .then(r => setLaunches(parsePromVector(r))).catch(() => setLaunchesError(true));

    setLogins(null); setLoginsError(false);
    getTopUsersByLogins(range, 10, filters)
      .then(r => setLogins(parsePromVector(r, 'user'))).catch(() => setLoginsError(true));

    setElevations(null); setElevationsError(false);
    getTopAppsByElevations(range, 10, filters)
      .then(r => setElevations(parsePromVector(r))).catch(() => setElevationsError(true));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filterKey, range, ready]);

  if (!ready) return null;

  return (
    <div className="panel-grid">
      <div className="chart-card">
        <h3 style={{ marginTop: 0, marginBottom: '1rem' }}>Most Active Apps</h3>
        <MiniBarList
          data={foreground} error={foregroundError}
          formatValue={v => `${v.toFixed(1)}h`}
          emptyMessage="No app usage in this period."
        />
      </div>
      <div className="chart-card">
        <h3 style={{ marginTop: 0, marginBottom: '1rem' }}>Most Launched Apps</h3>
        <MiniBarList data={launches} error={launchesError} emptyMessage="No launches in this period." />
      </div>
      <div className="chart-card">
        <h3 style={{ marginTop: 0, marginBottom: '1rem' }}>Users Signed In</h3>
        <MiniBarList data={logins} error={loginsError} emptyMessage="No logins in this period." />
      </div>
      <div className="chart-card">
        <h3 style={{ marginTop: 0, marginBottom: '1rem' }}>Privilege Elevations</h3>
        <MiniBarList data={elevations} error={elevationsError} emptyMessage="No privilege elevations in this period." />
      </div>
    </div>
  );
}
