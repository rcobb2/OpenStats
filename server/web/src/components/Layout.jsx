import { NavLink, Outlet, useLocation } from 'react-router-dom';

const navItems = [
  { to: '/', label: 'Dashboard' },
  { 
    to: '/agents', 
    label: 'Agents',
    children: [
      { to: '/agents', label: 'Monitor' },
      { to: '/agents/installers', label: 'Installers' },
      { to: '/agents/settings', label: 'Settings' },
    ]
  },
  { to: '/labs', label: 'Labs' },
  { to: '/mappings', label: 'Mappings' },
  { to: '/reports', label: 'Reports' },
];

export default function Layout() {
  const location = useLocation();

  return (
    <div className="app">
      <nav className="sidebar">
        <div className="sidebar-header">
          <h1>OpenLabStats</h1>
        </div>
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
                            className={({ isActive }) => isActive ? 'active' : ''}
                          >
                            {child.label}
                          </NavLink>
                        </li>
                      ))}
                    </ul>
                  </div>
                ) : (
                  <NavLink to={item.to} className={({ isActive }) => isActive ? 'active' : ''}>
                    {item.label}
                  </NavLink>
                )}
              </li>
            );
          })}
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
