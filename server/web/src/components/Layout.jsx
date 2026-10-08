import { useEffect, useState } from 'react';
import { NavLink, Outlet, useLocation } from 'react-router-dom';
import { getBuildInfo } from '../api';
import ErrorBoundary from './ErrorBoundary';

// Small hand-rolled line icons (no icon library dependency) — stroke-based
// so they inherit color/opacity from the nav link's own CSS rather than
// needing per-icon color props.
const icons = {
  dashboard: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="3" width="7" height="9" rx="1.5" /><rect x="14" y="3" width="7" height="5" rx="1.5" />
      <rect x="14" y="12" width="7" height="9" rx="1.5" /><rect x="3" y="16" width="7" height="5" rx="1.5" />
    </svg>
  ),
  agents: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="4" width="18" height="7" rx="1.5" /><rect x="3" y="13" width="18" height="7" rx="1.5" />
      <circle cx="7" cy="7.5" r="0.5" fill="currentColor" /><circle cx="7" cy="16.5" r="0.5" fill="currentColor" />
    </svg>
  ),
  installers: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 3v12" /><path d="m7 10 5 5 5-5" /><path d="M4 18h16" />
    </svg>
  ),
  settings: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
    </svg>
  ),
  labs: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M3 21h18" /><path d="M6 21V8l6-5 6 5v13" /><path d="M10 21v-6h4v6" /><path d="M10 11h.01" /><path d="M14 11h.01" />
    </svg>
  ),
  mappings: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M12.59 2.59a2 2 0 0 0-2.83 0L2.59 9.76a2 2 0 0 0 0 2.83l7.82 7.82a2 2 0 0 0 2.83 0l7.17-7.17a2 2 0 0 0 0-2.83z" />
      <circle cx="7.5" cy="7.5" r="1.25" fill="currentColor" stroke="none" />
    </svg>
  ),
  users: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2" /><circle cx="9" cy="7" r="4" />
      <path d="M22 21v-2a4 4 0 0 0-3-3.87" /><path d="M16 3.13a4 4 0 0 1 0 7.75" />
    </svg>
  ),
  reports: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M3 3v18h18" /><path d="M18 17V9" /><path d="M13 17V5" /><path d="M8 17v-3" />
    </svg>
  ),
};

const navItems = [
  { to: '/', label: 'Dashboard', icon: icons.dashboard },
  {
    to: '/agents',
    label: 'Agents',
    icon: icons.agents,
    children: [
      { to: '/agents', label: 'Monitor', icon: icons.agents },
      { to: '/agents/installers', label: 'Installers', icon: icons.installers },
      { to: '/agents/settings', label: 'Settings', icon: icons.settings },
    ]
  },
  // Labs now has a /labs/:id detail page too — same prefix-match reasoning
  // as Reports below.
  { to: '/labs', label: 'Labs', icon: icons.labs, end: false },
  { to: '/mappings', label: 'Mappings', icon: icons.mappings },
  { to: '/users', label: 'Users', icon: icons.users },
  // Reports has its own sub-routes (/reports/user, /reports/hardware, ...)
  // rather than a single page, so its link can't use exact-match `end` like
  // the other flat items — it needs a prefix match to stay highlighted
  // across all of them.
  { to: '/reports', label: 'Reports', icon: icons.reports, end: false },
];

// A nav group's "default" child (the one whose `to` equals the parent's own
// `to`, e.g. Monitor at /agents) needs to stay highlighted on a detail route
// under it too (/agents/:id), not just its own exact path — otherwise
// landing on AgentDetail highlights nothing in the sidebar at all, the only
// "you are here" cue left being a small inline back-link on the page itself.
// It must NOT claim a path a more specific sibling already owns (/agents/
// installers, /agents/settings), so this checks every sibling first and only
// falls through to the default child if none of them match.
function isChildActive(child, siblings, parentTo, pathname) {
  if (child.to !== parentTo) {
    return pathname === child.to || pathname.startsWith(child.to + '/');
  }
  if (pathname === child.to) return true;
  if (!pathname.startsWith(parentTo + '/')) return false;
  return !siblings.some(s => s.to !== parentTo && (pathname === s.to || pathname.startsWith(s.to + '/')));
}

export default function Layout() {
  const location = useLocation();
  const [buildInfo, setBuildInfo] = useState(null);

  useEffect(() => {
    getBuildInfo().then(setBuildInfo).catch(() => {});
  }, []);

  return (
    <div className="app">
      <nav className="sidebar">
        <div className="sidebar-header">
          <span className="logo-mark">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M3 3v18h18" /><path d="m19 9-5 5-4-4-4 4" />
            </svg>
          </span>
          <h1>OpenLabStats</h1>
        </div>
        <div className="nav-list">
        <ul>
          {navItems.map((item) => {
            const isParentActive = item.children && location.pathname.startsWith(item.to);

            return (
              <li key={item.to}>
                {item.children ? (
                  <div className={`nav-group ${isParentActive ? 'expanded' : ''}`}>
                    <span className="nav-group-label">{item.label}</span>
                    <ul className="sub-nav">
                      {item.children.map(child => (
                        <li key={child.to}>
                          <NavLink
                            to={child.to}
                            end={child.to === item.to}
                            className={
                              isChildActive(child, item.children, item.to, location.pathname) ? 'active' : ''
                            }
                          >
                            {child.icon}
                            {child.label}
                          </NavLink>
                        </li>
                      ))}
                    </ul>
                  </div>
                ) : (
                  <NavLink to={item.to} end={item.end !== false} className={({ isActive }) => isActive ? 'active' : ''}>
                    {item.icon}
                    {item.label}
                  </NavLink>
                )}
              </li>
            );
          })}
        </ul>
        </div>
        <div className="sidebar-footer">
          <a href="/api/docs/" target="_blank" rel="noreferrer">API Docs</a>
          {buildInfo && (
            <span className="build-info" title={`Built ${buildInfo.buildDate} · ${buildInfo.goVersion}`}>
              v{buildInfo.version} · {buildInfo.gitCommit.slice(0, 7)}
            </span>
          )}
        </div>
      </nav>
      <main className="content">
        {/* Scoped per-route (keyed on pathname) rather than wrapping the whole
            app: if one page's render throws, the sidebar stays usable and
            simply navigating elsewhere remounts this boundary with a clean
            slate — no need to hit "Try again" or lose the nav entirely. The
            app-level ErrorBoundary in main.jsx (outside Layout) is the
            fallback for a crash in Layout itself. */}
        <ErrorBoundary key={location.pathname}>
          <Outlet />
        </ErrorBoundary>
      </main>
    </div>
  );
}
