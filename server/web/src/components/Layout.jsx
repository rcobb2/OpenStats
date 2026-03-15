import { NavLink, Outlet } from 'react-router-dom';

const navItems = [
  { to: '/', label: 'Dashboard' },
  { to: '/agents', label: 'Agents' },
  { to: '/labs', label: 'Labs' },
  { to: '/mappings', label: 'Mappings' },
  { to: '/installer', label: 'Installer' },
  { to: '/reports', label: 'Reports' },
];

export default function Layout() {
  return (
    <div className="app">
      <nav className="sidebar">
        <div className="sidebar-header">
          <h1>OpenLabStats</h1>
        </div>
        <ul>
          {navItems.map(({ to, label }) => (
            <li key={to}>
              <NavLink to={to} className={({ isActive }) => isActive ? 'active' : ''}>
                {label}
              </NavLink>
            </li>
          ))}
        </ul>
        <div className="sidebar-footer">
          <a href="/api/docs/" target="_blank" rel="noreferrer">API Docs</a>
          <a href="http://localhost:3000" target="_blank" rel="noreferrer">Grafana</a>
        </div>
      </nav>
      <main className="content">
        <Outlet />
      </main>
    </div>
  );
}
