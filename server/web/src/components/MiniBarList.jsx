// Compact horizontal bar list for small top-N panels — lighter weight than a
// full Recharts bar chart (no axes/gridlines/tooltip) for a handful of rows,
// e.g. recent elevations on the Dashboard, or a single host's/lab's top apps.
export default function MiniBarList({
  data, error, emptyMessage = 'No data for this period.',
  formatValue = (v) => Math.round(v), colors = ['var(--accent)'],
}) {
  if (error) return <div className="error">Failed to load data.</div>;
  if (!data) return <div className="loading">Loading…</div>;
  if (data.length === 0) return <div className="empty">{emptyMessage}</div>;

  const max = Math.max(...data.map(d => d.value));

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
      {data.map((d, i) => (
        <div key={`${d.name}-${i}`} style={{ display: 'flex', alignItems: 'center', gap: '0.6rem' }}>
          <span style={{
            width: 150, fontSize: 13, color: 'var(--text)',
            overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', flexShrink: 0,
          }}>
            {d.name}
          </span>
          <div style={{ flex: 1, height: 8, background: 'var(--surface-2)', borderRadius: 4, overflow: 'hidden' }}>
            <div style={{
              width: `${max > 0 ? (d.value / max) * 100 : 0}%`,
              height: '100%',
              background: colors[i % colors.length],
              borderRadius: 4,
            }} />
          </div>
          <span style={{ fontSize: 12, color: 'var(--text-dim)', width: 48, textAlign: 'right', flexShrink: 0 }}>
            {formatValue(d.value)}
          </span>
        </div>
      ))}
    </div>
  );
}
