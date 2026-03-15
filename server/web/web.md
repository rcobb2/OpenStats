# Frontend Component Documentation

The OpenLabStats frontend is a React + Vite application that provides a web UI for managing the agent fleet, software mappings, labs, and viewing usage reports.

## Overview

The frontend:
1. Provides UI for agent fleet management
2. Allows lab/room organization
3. Manages software name mappings
4. Displays usage reports and dashboards
5. Generates customized agent installers

## Project Structure

```
server/web/
├── src/
│   ├── api.js           # API client functions
│   ├── main.jsx         # App entry, routing
│   ├── App.jsx          # Root component
│   ├── styles.css       # Global styles
│   ├── pages/           # Page components
│   │   ├── Dashboard.jsx
│   │   ├── Labs.jsx
│   │   ├── Agents.jsx
│   │   ├── Mappings.jsx
│   │   ├── Reports.jsx
│   │   └── Installer.jsx
│   └── components/      # Shared components
│       ├── Layout.jsx   # Nav + shell
│       └── Table.jsx   # ResizableTable wrapper
├── index.html
├── package.json
└── vite.config.js
```

## API Client (`src/api.js`)

Central API client using fetch:

```javascript
// Agents
getAgents()
getAgent(id)
deleteAgent(id)
assignAgentToLab(agentId, labId)

// Labs
getLabs()
createLab(data)
updateLab(id, data)
deleteLab(id)

// Mappings
getMappings()
createMapping(data)
updateMapping(data)
deleteMapping(id)

// Reports
getSummary()
getTopApps(range)
getUsageByLab(range)
getActiveUsers()

// Installers
generateInstaller(data)

getSettings()
```

Base URL: `/api/v1` (proxied by server)

## Pages

### Dashboard (`pages/Dashboard.jsx`)
- Overview metrics summary
- Active agents count
- Active users count
- Top apps quick view

### Labs (`pages/Labs.jsx`)
- List all labs
- Create/edit/delete labs
- Fields: name, building, room, description

### Agents (`pages/Agents.jsx`)
- List all registered agents
- Show status (online/offline)
- Assign to lab
- Delete agent
- Columns: hostname, IP, OS, version, lab, status, last seen

### Mappings (`pages/Mappings.jsx`)
- List software name mappings
- Create/edit/delete mappings
- Fields: exe name, display name, category, publisher, family

### Reports (`pages/Reports.jsx`)
- Top applications by usage time
- Usage by lab
- Active users
- Time range selector (24h, 7d, 30d)

### Installer (`pages/Installer.jsx`)
- Generate customized agent installer
- Configure: server address, agent port
- Download generated installer

## Components

### Layout (`components/Layout.jsx`)
- Sidebar navigation
- Page title
- Routes: Dashboard, Labs, Agents, Mappings, Reports, Installer

### Table (`components/Table.jsx`)
- `ResizableTable` component
- Automatically adds resizable handles to all column headers
- Maintains `table-layout: fixed` for stable resizing
- Hover effects for handle visibility

## Routing (`main.jsx`)

Uses React Router:

```jsx
<Routes>
  <Route path="/" element={<Layout />}>
    <Route index element={<Dashboard />} />
    <Route path="labs" element={<Labs />} />
    <Route path="agents" element={<Agents />} />
    <Route path="mappings" element={<Mappings />} />
    <Route path="reports" element={<Reports />} />
    <Route path="installer" element={<Installer />} />
  </Route>
</Routes>
```

## Building

```powershell
cd server/web
npm install
npm run build
```

## Development

```powershell
cd server/web
npm run dev
```

Dev server proxies API requests to server backend.

## Configuration

Vite config (`vite.config.js`):
- Proxies `/api` to `http://localhost:8080`
- Host: configurable

## Dependencies

- React 18+
- Vite
- React Router DOM
- (No external UI library - custom CSS)

## Common Tasks

### Adding a New Page

1. Create `src/pages/NewPage.jsx`
2. Add route in `main.jsx`
3. Add nav link in `components/Layout.jsx`
4. Add API functions if needed in `api.js`

### Adding API Function

Add to `src/api.js`:

```javascript
export const getNewData = () => request('/new-endpoint');
```

### Modifying Table

Use reusable `Table` component in `components/Table.jsx`:

```jsx
<Table
  columns={[{ key: 'name', label: 'Name' }]}
  data={items}
  onEdit={handleEdit}
  onDelete={handleDelete}
/>
```
