package logrotate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteBelowCapDoesNotRotate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.log")

	w, err := New(path, 1024, 3)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	if _, err := w.Write([]byte("hello\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("expected no backup file yet, stat err=%v", err)
	}
}

func TestRotatesOnceCapExceeded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.log")

	w, err := New(path, 10, 2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	line := []byte("0123456789\n") // 11 bytes, exceeds the 10-byte cap alone
	for i := 0; i < 3; i++ {
		if _, err := w.Write(line); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected a .1 backup after rotation, got err=%v", err)
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("expected no .3 backup beyond maxBackups=2, stat err=%v", err)
	}
}

func TestOversizedExistingFileIsTruncatedNotBackedUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.log")

	// Simulate a pre-existing runaway log file, well over the configured cap.
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 1000)), 0644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	w, err := New(path, 100, 3)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat current: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("expected oversized existing file to be truncated to 0 bytes, got %d", info.Size())
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("expected the oversized file NOT to be preserved as a .1 backup, stat err=%v", err)
	}
}

func TestTruncateIfOversized(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "redirected.log")

	// Missing file: not an error, nothing truncated.
	truncated, err := TruncateIfOversized(path, 100)
	if err != nil {
		t.Fatalf("missing file: %v", err)
	}
	if truncated {
		t.Fatalf("expected no truncation for a missing file")
	}

	// Under the cap: left alone.
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 50)), 0644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	truncated, err = TruncateIfOversized(path, 100)
	if err != nil {
		t.Fatalf("under cap: %v", err)
	}
	if truncated {
		t.Fatalf("expected no truncation while under the cap")
	}

	// Over the cap: truncated to zero.
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 1000)), 0644); err != nil {
		t.Fatalf("grow file: %v", err)
	}
	truncated, err = TruncateIfOversized(path, 100)
	if err != nil {
		t.Fatalf("over cap: %v", err)
	}
	if !truncated {
		t.Fatalf("expected truncation when over the cap")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after truncate: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("expected file truncated to 0 bytes, got %d", info.Size())
	}
}

func TestMaxBackupsZeroDeletesInsteadOfRotating(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.log")

	w, err := New(path, 10, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	line := []byte("0123456789\n")
	for i := 0; i < 3; i++ {
		if _, err := w.Write(line); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}

	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("expected no backups kept when maxBackups=0, stat err=%v", err)
	}
}
