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
│   ├── styles.css       # Global styles (design tokens + shared classes)
│   ├── pages/           # Page components
│   │   ├── Dashboard.jsx
│   │   ├── Labs.jsx
│   │   ├── LabDetail.jsx       # /labs/:id — one lab's own usage + its agents
│   │   ├── Mappings.jsx
│   │   ├── Users.jsx
│   │   ├── Reports.jsx         # Shell + report-type routing; see below
│   │   └── agents/
│   │       ├── AgentsList.jsx  # "Monitor" — fleet list, status filter, search
│   │       ├── AgentDetail.jsx # /agents/:id — one machine's own usage
│   │       ├── Installer.jsx
│   │       └── Settings.jsx
│   ├── hooks/
│   │   └── useGlobalFilters.js # Shared range/machine/lab scope, URL-persisted
│   └── components/      # Shared components
│       ├── Layout.jsx          # Nav + shell
│       ├── Table.jsx           # ResizableTable wrapper
│       ├── ErrorBoundary.jsx
│       ├── GlobalFilterBar.jsx # Range + Machine/Lab controls, used by Dashboard + Reports
│       └── MiniBarList.jsx     # Compact top-N bar list, used by 3+ pages
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

// Users
getUsers(), getUserRules(), saveUserRule(), deleteUserRule(),
patchUserRuleIgnore(), ignoreUser(), getUserPolicy(), updateUserPolicy()

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

// Settings
getSettings()
updateSettings(data)
```

Base URL: `/api/v1` (proxied by server)

## Pages

### Dashboard (`pages/Dashboard.jsx`)
- `TriageBanner` — the first thing on the page, above even the filter bar:
  a single quiet line ("All N agents online and up to date") when the fleet
  is healthy, or a prominent alert block (counts + a chip list of affected
  hostnames linking to `AgentDetail`, capped at 8 with a "+N more" link to
  the Agents page) when something needs attention. Modeled on how Tenable/
  Datadog put "what's Critical right now" ahead of routine metrics rather
  than burying it in a chart grid. Deliberately **not** scoped by the lab
  filter (see the comment on `TriageBanner` — a triage panel shouldn't let a
  lab filter hide an offline machine elsewhere)
- Stat cards: total/online agents, labs, mappings, active users — each links
  to the corresponding page (`/agents`, `/labs`, `/mappings`, `/users`)
- Uses `useGlobalFilters` + `GlobalFilterBar` (range + lab, no machine scope) —
  same URL-persisted state Reports uses, so a Dashboard link with `?range=`/
  `&lab=` is shareable and survives a refresh
- Top Applications by Launch Count, Usage by Lab, Recent Privilege Elevations
  — one `panel-grid` (previously two, with Fleet Health as a fourth panel;
  removed now that it's `TriageBanner` instead). Usage by Lab's bars are
  clickable — navigates to that lab's `LabDetail` page (no-op for the
  synthetic "Unassigned" bucket, which isn't a real entity)
- Recent Privilege Elevations (top apps by elevation count) — uses the shared
  `MiniBarList` component

### Labs (`pages/Labs.jsx`)
- List all labs
- Create/edit/delete labs
- Fields: name, building, room, description
- Lab name links to `LabDetail` (`/labs/:id`)

### Lab Detail (`pages/LabDetail.jsx`)
- Reached by clicking a lab name anywhere (Labs list, Dashboard's Usage by
  Lab chart). Route: `/labs/:id`
- Metadata: building, room, description
- Same four `MiniBarList` panels as `AgentDetail`, scoped to this lab via the
  `lab` report filter (by name, not id — the backend's lab-scoped queries key
  on the lab's name string)
- A table of this lab's agents (filtered client-side from `getAgents()` by
  `labId`), with hostnames linking to `AgentDetail`
- Uses `GlobalFilterBar` with `showMachine={false} showLab={false}` — same
  reasoning as `AgentDetail`

### Agents

#### Monitor (`pages/agents/AgentsList.jsx`)
- List all registered agents
- Status filter tabs (All/Online/Outdated/Offline, with live counts) + a
  hostname/IP/lab search box — status is computed server-side (see
  `applyEffectiveStatus` in `server/internal/api/agents.go`): "offline" means
  `lastSeen` exceeds the configured stale timeout, not a value the agent
  itself ever reports
- Hostname links to `AgentDetail` (`/agents/:id`)
- Assign to lab
- Delete agent, force an individual agent's update
- Columns: hostname, IP, OS, version, lab, status, last seen

#### Agent Detail (`pages/agents/AgentDetail.jsx`)
- Reached by clicking a hostname anywhere (Agents list, Dashboard's Fleet
  Health). Route: `/agents/:id`
- Metadata: IP, OS version, agent version, status badge, lab (links to
  `LabDetail`), last seen
- Four `MiniBarList` panels scoped to just this machine via the `hostname`
  report filter: Most Active Apps, Most Launched Apps, Users Signed In,
  Privilege Elevations
- Uses `GlobalFilterBar` with `showMachine={false} showLab={false}` — scope
  is already fixed to this one host, so only Time Range is exposed

#### Installers (`pages/agents/Installer.jsx`)
- Generate customized agent installer
- Configure: server address, agent port, default building/room
- Shows silent deployment commands

#### Settings (`pages/agents/Settings.jsx`)
- Fleet-wide configuration
- Heartbeat interval
- Force agent version updates
- Stale agent cleanup threshold
### Mappings (`pages/Mappings.jsx`)
- List software name mappings
- Create/edit/delete mappings
- Fields: exe name, display name, category, publisher, family

### Users (`pages/Users.jsx`)
- Lists every username seen in metrics, grouped by the canonical identity it
  resolves to (`Seen As` shows the raw names that were merged)
- Ignore/unignore accounts such as service accounts (e.g. `zabbix`)
- Merge an identity into another username, for cases where a macOS shortname
  differs from the AD account name
- Toggles cross-platform correlation (domain/UPN stripping) fleet-wide
- Tabs: All, Tracked, Ignored, Merged, Rules

### Reports (`pages/Reports.jsx`)
Each report type is its own route — `/reports/user`, `/reports/hardware`,
`/reports/software`, `/reports/elevations` (bare `/reports` redirects to
`/reports/user`, preserving any query string) — not client-side-only dropdown
state. `type` comes from `useParams()`; an unrecognized value redirects to
`/reports/user`. Switching between them is a `.tab-bar` of `NavLink`s that
explicitly carry the current `location.search` along, so range/machine/lab
scope survives the navigation. `type` picks which component renders:
`UserBehaviorReport`, `LabUsageReport` (hardware), `SoftwareMeteringReport`,
`ElevationReport`.

Time range, custom date range, and machine/lab scope are **not** local state —
they come from `useGlobalFilters()` (`src/hooks/useGlobalFilters.js`), which
reads/writes them as URL query params (`?range=`, `&start=`/`&end=`,
`&hostname=`/`&lab=`) shared with the Dashboard. The `GlobalFilterBar`
component renders the actual controls; Reports adds its own App-name filter
(`appFilter`, client-side only, not in the URL) as an extra child control.
`chartKey` still forces each report body to remount on any filter change,
same as before.

- Top applications by usage time
- Usage by lab
- Active users
- **Privilege Elevations** (`ElevationReport`) — its own report type, not a
  panel under User Behavior: Top Elevated Apps, Top Users by Elevations. UAC
  on Windows, sudo/admin authorization on macOS. Honors the shared range
  selector like every other panel (a forced 30-day floor for sparse
  login-derived panels was tried and then removed — see git history on
  Reports.jsx — since it hid genuinely sparse data instead of showing it).


## Components

### Layout (`components/Layout.jsx`)
- Sidebar navigation
- Page title
- Routes: Dashboard, Labs, Agents, Mappings, Users, Reports, Installer
- Both the Reports and Labs nav items use prefix matching (`end: false` in
  `navItems`), not exact-match like every other flat item — they point at
  `/reports` and `/labs` respectively, which need to also match their detail
  sub-routes (`/reports/:type`, `/labs/:id`) for the sidebar to stay
  highlighted while on one. Every other flat item still uses exact match
  (`end: true`, the default); only an item with real sub-routes needs the
  prefix form. (Agents has the same issue for `/agents/:id`, but its sidebar
  entry is a `children` group, not a flat item — `isParentActive` already
  does prefix matching for the group, so no change was needed there; no
  individual sub-nav item highlights while on an agent's detail page, which
  is an acceptable gap since detail pages aren't themselves in the nav.)

### GlobalFilterBar (`components/GlobalFilterBar.jsx`)
- Renders Time Range (+ custom From/To when `range === 'custom'`), and
  optional Machine/Lab selects (`showMachine`/`showLab` props, both default
  true)
- Takes a `filters` prop — the object returned by `useGlobalFilters()` — plus
  `agents`/`labs` lists the caller already fetched for other reasons, so this
  component doesn't do its own independent (and potentially racing) fetch
- Accepts `children` for a page's own extra controls alongside the shared
  ones (Reports' App-name filter)
- `AgentDetail`/`LabDetail` pass `showMachine={false} showLab={false}` —
  scope is already fixed to one entity, so only Time Range is shown

### MiniBarList (`components/MiniBarList.jsx`)
- Compact horizontal bar list for small top-N panels — no axes/gridlines/
  tooltip, unlike the Recharts-based charts elsewhere
- Used by Dashboard's Recent Privilege Elevations panel and both
  `AgentDetail`/`LabDetail`'s four usage panels (extracted once a third
  usage appeared — see the comment at the top of the file)
- Props: `data`/`error` (same null/false/array convention as every other
  chart component in this app), `emptyMessage`, `formatValue` (defaults to
  `Math.round`), `colors` (defaults to a single accent color; Dashboard
  passes its `CHART_COLORS` array for per-row color variation)
- Used by Dashboard (`showMachine={false}` — machine-level scope doesn't fit
  a fleet overview) and Reports (all three scopes)

### Table (`components/Table.jsx`)
- `ResizableTable` component
- Automatically adds resizable handles to all column headers
- Maintains `table-layout: fixed` for stable resizing
- Hover effects for handle visibility

## Shared Filter State (`hooks/useGlobalFilters.js`)

Time range + machine/lab scope used to be local `useState` duplicated in both
Dashboard.jsx and Reports.jsx (and not reset-safe — refreshing the page or
switching report types threw it away). `useGlobalFilters()` instead reads and
writes it as URL query params via `useSearchParams`:

- `?range=` (default `24h`, omitted from the URL when default)
- `&start=`/`&end=` (datetime-local strings, only meaningful when `range=custom`)
- `&hostname=` / `&lab=` — mutually exclusive; setting one clears the other

Returns `{ range, setRange, customStart, setCustomStart, customEnd,
setCustomEnd, hostname, setHostname, lab, setLab, isCustomReady, filters,
effectiveRange }` — `filters` is the memoized `{start,end,hostname,lab}`
object the `api.js` report functions already expect; `effectiveRange` is
either the preset string or `"<start>~<end>"` for a custom range. Because
this is just a thin wrapper over the URL, two pages reading it at once (e.g.
Dashboard and Reports, or two browser tabs) share the same state for free,
and any filtered view is a real, shareable link.

## Routing (`main.jsx`)

Uses React Router:

```jsx
<Routes>
  <Route path="/" element={<Layout />}>
    <Route index element={<Dashboard />} />
    <Route path="agents" element={<AgentsList />} />
    <Route path="agents/installers" element={<Installer />} />
    <Route path="agents/settings" element={<Settings />} />
    <Route path="agents/:id" element={<AgentDetail />} />
    <Route path="labs" element={<Labs />} />
    <Route path="labs/:id" element={<LabDetail />} />
    <Route path="mappings" element={<Mappings />} />
    <Route path="users" element={<Users />} />
    <Route path="reports" element={<ReportsIndexRedirect />} />
    <Route path="reports/:type" element={<Reports />} />
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
