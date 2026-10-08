import { useState } from 'react';

// A plain <select> paired with a live filter text input above it, for a list
// long enough that scrolling its full native option list is unusable (the
// Machine filter has one option per agent — 739 in production at the time
// this was added; Lab has 175). Filtering only narrows which <option>s
// render; the select's own value/onChange contract is completely
// unchanged, so this is a drop-in wrapper, not a new control with new
// failure modes — nothing here can produce a value the caller's onChange
// wasn't already prepared to receive from a plain <select>.
//
// The filter box only appears once the list is actually long enough to need
// it (more than `threshold` options), so this is a no-op visually for any
// select with a short, already-manageable option list.
export default function FilterableSelect({
  options, getValue, getLabel, value, onChange, allOption, threshold = 15,
  filterPlaceholder = 'Filter…', selectProps = {},
}) {
  const [query, setQuery] = useState('');
  const q = query.trim().toLowerCase();

  // Always keep the currently selected option visible even if it doesn't
  // match the filter — otherwise picking a result then refining the filter
  // text would make the select's own displayed value look wrong (the
  // browser can't render a selected option that isn't in the list).
  const filtered = q
    ? options.filter(o => getLabel(o).toLowerCase().includes(q) || getValue(o) === value)
    : options;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '0.2rem' }}>
      {options.length > threshold && (
        <input
          type="text"
          placeholder={filterPlaceholder}
          value={query}
          onChange={e => setQuery(e.target.value)}
          style={{ fontSize: '0.78rem', padding: '0.2rem 0.4rem' }}
        />
      )}
      <select value={value} onChange={onChange} {...selectProps}>
        {allOption && <option value={allOption.value}>{allOption.label}</option>}
        {filtered.map(o => (
          <option key={getValue(o)} value={getValue(o)}>{getLabel(o)}</option>
        ))}
      </select>
    </div>
  );
}
