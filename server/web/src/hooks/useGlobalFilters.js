import { useMemo } from 'react';
import { useSearchParams } from 'react-router-dom';

// Default custom-range bounds: last 24h, formatted for a datetime-local input.
function defaultDatetime(offsetHours = 0) {
  const d = new Date(Date.now() - offsetHours * 3600 * 1000);
  return d.toISOString().slice(0, 16);
}

// Time range + machine/lab scope, shared by every metrics page (Dashboard,
// each Reports sub-page) and persisted in the URL query string rather than
// component state. A filtered view is then a real, shareable link — switching
// between report types or reloading the page no longer resets range/scope
// back to defaults, and two tabs can point at two different saved views.
export function useGlobalFilters() {
  const [params, setParams] = useSearchParams();

  const range = params.get('range') || '24h';
  const customStart = params.get('start') || defaultDatetime(24);
  const customEnd = params.get('end') || defaultDatetime(0);
  const hostname = params.get('hostname') || '';
  const lab = params.get('lab') || '';

  const update = (patch) => {
    setParams(prev => {
      const next = new URLSearchParams(prev);
      for (const [k, v] of Object.entries(patch)) {
        if (v) next.set(k, v); else next.delete(k);
      }
      return next;
    }, { replace: true });
  };

  const setRange = (v) => update({ range: v === '24h' ? null : v });
  const setCustomStart = (v) => update({ start: v });
  const setCustomEnd = (v) => update({ end: v });
  // Machine and lab scope are mutually exclusive — picking one clears the other.
  const setHostname = (v) => update({ hostname: v || null, lab: v ? null : lab });
  const setLab = (v) => update({ lab: v || null, hostname: v ? null : hostname });

  const isCustomReady = range === 'custom' && customStart && customEnd
    && new Date(customEnd) > new Date(customStart);

  // Memoized so object identity only changes when a value actually changes,
  // preventing consumers' useEffects from re-firing on every render.
  const filters = useMemo(() => ({
    ...(isCustomReady ? { start: customStart, end: customEnd } : {}),
    ...(hostname ? { hostname } : {}),
    ...(lab ? { lab } : {}),
  }), [isCustomReady, customStart, customEnd, hostname, lab]);

  const effectiveRange = isCustomReady ? `${customStart}~${customEnd}` : range;

  return {
    range, setRange,
    customStart, setCustomStart,
    customEnd, setCustomEnd,
    hostname, setHostname,
    lab, setLab,
    isCustomReady, filters, effectiveRange,
  };
}
