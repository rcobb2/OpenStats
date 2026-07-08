import { useRef, useEffect } from 'react';

export default function ResizableTable({ children, className = "" }) {
  const tableRef = useRef(null);

  useEffect(() => {
    const table = tableRef.current;
    if (!table) return;

    const headers = Array.from(table.querySelectorAll('th'));
    // Track active drag handlers per header so we can remove them on unmount.
    const active = new Map();

    headers.forEach((th) => {
      if (th.querySelector('.resizer')) return;

      const resizer = document.createElement('div');
      resizer.classList.add('resizer');
      th.appendChild(resizer);

      const onMouseMove = (e) => {
        const state = active.get(th);
        if (state) th.style.width = `${state.w + e.clientX - state.x}px`;
      };

      const onMouseUp = () => {
        resizer.classList.remove('resizing');
        document.removeEventListener('mousemove', onMouseMove);
        document.removeEventListener('mouseup', onMouseUp);
        active.delete(th);
      };

      resizer.addEventListener('mousedown', (e) => {
        active.set(th, { x: e.clientX, w: parseInt(window.getComputedStyle(th).width, 10), onMouseMove, onMouseUp });
        resizer.classList.add('resizing');
        document.addEventListener('mousemove', onMouseMove);
        document.addEventListener('mouseup', onMouseUp);
      });
    });

    return () => {
      active.forEach(({ onMouseMove, onMouseUp }) => {
        document.removeEventListener('mousemove', onMouseMove);
        document.removeEventListener('mouseup', onMouseUp);
      });
    };
  }, [children]);

  return (
    <div className="table-wrapper" style={{ overflowX: 'auto' }}>
      <table ref={tableRef} className={className}>
        {children}
      </table>
    </div>
  );
}

