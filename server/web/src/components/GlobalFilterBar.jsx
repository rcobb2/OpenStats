import FilterableSelect from './FilterableSelect';

const labelStyle = { color: 'var(--text-dim)', fontSize: '0.85rem', marginRight: '0.3rem' };
const ctrlStyle = { display: 'flex', alignItems: 'center', gap: '0.3rem' };

// Shared time-range + machine/lab scope controls, backed by useGlobalFilters
// so every page using this reads and writes the same URL-persisted state.
// `agents`/`labs` are passed in by the caller (each page already needs its
// own copy of these lists for other reasons) rather than fetched here, to
// avoid a second independent fetch racing the page's own.
export default function GlobalFilterBar({
  filters, agents = [], labs = [], showMachine = true, showLab = true, children,
}) {
  const {
    range, setRange, customStart, setCustomStart, customEnd, setCustomEnd,
    hostname, setHostname, lab, setLab,
  } = filters;

  return (
    <div className="filter-bar">
      <div style={ctrlStyle}>
        <label style={labelStyle}>Time Range</label>
        <select value={range} onChange={e => setRange(e.target.value)}>
          <option value="1h">Last Hour</option>
          <option value="24h">Last 24 Hours</option>
          <option value="7d">Last 7 Days</option>
          <option value="30d">Last 30 Days</option>
          <option value="custom">Custom…</option>
        </select>
      </div>

      {range === 'custom' && (
        <>
          <div style={ctrlStyle}>
            <label style={labelStyle}>From</label>
            <input
              type="datetime-local"
              value={customStart}
              onChange={e => setCustomStart(e.target.value)}
              style={{ fontSize: '0.85rem' }}
            />
          </div>
          <div style={ctrlStyle}>
            <label style={labelStyle}>To</label>
            <input
              type="datetime-local"
              value={customEnd}
              onChange={e => setCustomEnd(e.target.value)}
              style={{ fontSize: '0.85rem' }}
            />
          </div>
        </>
      )}

      {showMachine && (
        <div style={ctrlStyle}>
          <label style={labelStyle}>Machine</label>
          <FilterableSelect
            options={agents}
            getValue={a => a.hostname}
            getLabel={a => a.hostname}
            value={hostname}
            onChange={e => setHostname(e.target.value)}
            allOption={{ value: '', label: 'All Machines' }}
            filterPlaceholder="Filter machines…"
          />
        </div>
      )}

      {showLab && (
        <div style={ctrlStyle}>
          <label style={labelStyle}>Lab</label>
          <FilterableSelect
            options={labs}
            getValue={l => l.name}
            getLabel={l => l.name}
            value={lab}
            onChange={e => setLab(e.target.value)}
            allOption={{ value: '', label: 'All Labs' }}
            filterPlaceholder="Filter labs…"
          />
        </div>
      )}

      {children}
    </div>
  );
}
