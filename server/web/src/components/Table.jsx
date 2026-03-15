import { useRef, useEffect } from 'react';

export default function ResizableTable({ children, className = "" }) {
  const tableRef = useRef(null);

  useEffect(() => {
    const table = tableRef.current;
    if (!table) return;

    // Wait a bit for children to render if needed, or re-run on children change
    const headers = table.querySelectorAll('th');
    headers.forEach((th) => {
      if (th.querySelector('.resizer')) return;

      const resizer = document.createElement('div');
      resizer.classList.add('resizer');
      th.appendChild(resizer);

      let x = 0;
      let w = 0;

      const onMouseMove = (e) => {
        const dx = e.clientX - x;
        th.style.width = `${w + dx}px`;
      };

      const onMouseUp = () => {
        resizer.classList.remove('resizing');
        document.removeEventListener('mousemove', onMouseMove);
        document.removeEventListener('mouseup', onMouseUp);
      };

      const onMouseDown = (e) => {
        x = e.clientX;
        const styles = window.getComputedStyle(th);
        w = parseInt(styles.width, 10);

        resizer.classList.add('resizing');
        document.addEventListener('mousemove', onMouseMove);
        document.addEventListener('mouseup', onMouseUp);
      };

      resizer.addEventListener('mousedown', onMouseDown);
    });
  }, [children]);

  return (
    <div className="table-wrapper" style={{ overflowX: 'auto' }}>
      <table ref={tableRef} className={className}>
        {children}
      </table>
    </div>
  );
}

