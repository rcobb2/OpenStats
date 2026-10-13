// Package logrotate provides a minimal size-based rotating file writer for
// the agent's log file. Without it, an append-only log file has no ceiling
// over a long agent uptime — this is what let agent.log reach 100+ GB on
// some lab machines once a per-tick debug-logging bug (since fixed, see
// agent/internal/monitor/proc_darwin.go and foreground_poller.go) drove the
// growth rate up.
package logrotate

import (
	"fmt"
	"os"
	"sync"
)

// Writer is an io.WriteCloser that rotates the underlying file once it
// exceeds MaxBytes, keeping at most MaxBackups rotated generations
// (path.1 is the most recent, path.N the oldest).
type Writer struct {
	mu         sync.Mutex
	path       string
	maxBytes   int64
	maxBackups int
	f          *os.File
	size       int64
}

// New opens (creating if necessary) the log file at path for appending,
// rotating according to maxBytes/maxBackups.
//
// If the file already exceeds maxBytes at open time, it is truncated in
// place rather than shifted into the backup chain: a file already over the
// cap on startup isn't a normal rotation boundary, it's leftover runaway
// growth from before rotation existed (or before a logging bug was fixed),
// and preserving it as a multi-gigabyte ".1" backup would just move the
// disk problem sideways instead of reclaiming the space.
func New(path string, maxBytes int64, maxBackups int) (*Writer, error) {
	w := &Writer{path: path, maxBytes: maxBytes, maxBackups: maxBackups}
	if err := w.openCurrent(os.O_CREATE | os.O_WRONLY | os.O_APPEND); err != nil {
		return nil, err
	}
	if w.size > w.maxBytes {
		if err := w.openCurrent(os.O_CREATE | os.O_WRONLY | os.O_TRUNC); err != nil {
			return nil, err
		}
	}
	return w, nil
}

func (w *Writer) openCurrent(flag int) error {
	if w.f != nil {
		w.f.Close()
	}
	f, err := os.OpenFile(w.path, flag, 0644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.f = f
	w.size = info.Size()
	return nil
}

func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate shifts path.(N-1) -> path.N down the chain (dropping anything
// beyond maxBackups), moves the current file to path.1, and opens a fresh
// current file.
func (w *Writer) rotate() error {
	if w.f != nil {
		w.f.Close()
		w.f = nil
	}
	if w.maxBackups <= 0 {
		if err := os.Remove(w.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return w.openCurrent(os.O_CREATE | os.O_WRONLY | os.O_APPEND)
	}

	oldest := fmt.Sprintf("%s.%d", w.path, w.maxBackups)
	if err := os.Remove(oldest); err != nil && !os.IsNotExist(err) {
		return err
	}
	for i := w.maxBackups - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", w.path, i)
		dst := fmt.Sprintf("%s.%d", w.path, i+1)
		if err := os.Rename(src, dst); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return w.openCurrent(os.O_CREATE | os.O_WRONLY | os.O_APPEND)
}

// TruncateIfOversized truncates the file at path to zero bytes if it exceeds
// maxBytes, and reports whether it did. It's for files this process doesn't
// hold open itself — e.g. an OS service supervisor's stdout/stderr redirect
// (launchd's StandardOutPath/StandardErrorPath on macOS) — where a Writer's
// own open file handle isn't available to rotate. Truncating a file out from
// under another process that holds it open in append mode is safe and is
// the standard "copytruncate" pattern: the next append-mode write seeks to
// the new (zero) end of file, not the old one.
//
// A missing file is not an error (nothing to truncate yet).
func TruncateIfOversized(path string, maxBytes int64) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if info.Size() <= maxBytes {
		return false, nil
	}
	if err := os.Truncate(path, 0); err != nil {
		return false, err
	}
	return true, nil
}

func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
