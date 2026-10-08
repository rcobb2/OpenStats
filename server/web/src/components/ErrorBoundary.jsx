import React from 'react';

// Two things make a caught render error a dead end rather than a recoverable
// one, and both are fixed here:
//
// 1. "Try again" only cleared this boundary's own state and re-rendered the
//    exact same `children` element — if the crash came from persistent state
//    or props (not a one-off glitch), the exact same render runs again and
//    throws again, with no way to actually retry. `resetCount` is bumped on
//    every retry and used as `children`'s `key`, forcing React to unmount and
//    remount the whole subtree with fresh component state instead of just
//    reconciling the broken one.
// 2. There was no escape hatch: if retrying doesn't clear a persistent
//    error, the user had no way to leave the crashed page other than editing
//    the URL bar by hand. See Layout.jsx for the primary fix (this boundary
//    is scoped per-route there, so the sidebar nav survives a page crash and
//    simply clicking elsewhere clears it) — this escape link is the fallback
//    for the one other place this boundary is used: wrapping the whole
//    <Routes> tree in main.jsx, where even the sidebar is gone.
export default class ErrorBoundary extends React.Component {
  constructor(props) {
    super(props);
    this.state = { error: null, resetCount: 0 };
  }

  static getDerivedStateFromError(error) {
    return { error };
  }

  componentDidCatch(error, info) {
    // React already prints this in dev; in a production build this is the
    // only trace of the component stack that reaches the browser console.
    console.error('ErrorBoundary caught:', error, info.componentStack);
  }

  render() {
    if (this.state.error) {
      return (
        <div className="error-banner" style={{ margin: '2rem', padding: '1.5rem' }}>
          <h2 style={{ marginTop: 0, color: 'var(--danger)' }}>Something went wrong</h2>
          <pre style={{ fontSize: '0.8rem', whiteSpace: 'pre-wrap' }}>
            {this.state.error.message}
          </pre>
          <div style={{ display: 'flex', gap: '0.6rem', marginTop: '0.75rem' }}>
            <button onClick={() => this.setState(s => ({ error: null, resetCount: s.resetCount + 1 }))}>
              Try again
            </button>
            {/* .btn-secondary only sets color/background/border — padding,
                radius, weight and display normally come from the plain
                `button` element selector, which an <a> doesn't match, so
                they're supplied inline here (values match .btn-primary's
                own self-contained anchor styling in styles.css). */}
            <a
              href="/"
              className="btn-secondary"
              style={{
                padding: '0.55rem 1.3rem', borderRadius: 'var(--radius-sm)',
                fontWeight: 600, fontSize: '0.87rem',
                textDecoration: 'none', display: 'inline-block',
              }}
            >
              Go to Dashboard
            </a>
          </div>
        </div>
      );
    }
    return <React.Fragment key={this.state.resetCount}>{this.props.children}</React.Fragment>;
  }
}
