import React from 'react';

export default class ErrorBoundary extends React.Component {
  constructor(props) {
    super(props);
    this.state = { error: null };
  }

  static getDerivedStateFromError(error) {
    return { error };
  }

  render() {
    if (this.state.error) {
      return (
        <div className="error-banner" style={{ margin: '2rem', padding: '1.5rem' }}>
          <h2 style={{ marginTop: 0, color: 'var(--danger)' }}>Something went wrong</h2>
          <pre style={{ fontSize: '0.8rem', whiteSpace: 'pre-wrap' }}>
            {this.state.error.message}
          </pre>
          <button onClick={() => this.setState({ error: null })}>Try again</button>
        </div>
      );
    }
    return this.props.children;
  }
}
