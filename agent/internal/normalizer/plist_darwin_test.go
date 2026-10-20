//go:build darwin

package normalizer

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

const smallPlist = `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleName</key><string>Foo</string></dict></plist>`

func TestReadPlistDictSafety(t *testing.T) {
	dir := t.TempDir()

	good := filepath.Join(dir, "good.plist")
	os.WriteFile(good, []byte(smallPlist), 0o644)
	m, err := readPlistDict(good)
	if err != nil || m["CFBundleName"] != "Foo" {
		t.Fatalf("good plist: %v %v", m, err)
	}

	fifo := filepath.Join(dir, "fifo.plist")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.plist")
	os.Symlink(good, link)
	big := filepath.Join(dir, "big.plist")
	os.WriteFile(big, make([]byte, maxPlistSize+10), 0o644)

	for _, p := range []string{fifo, link, big} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			m, err := readPlistDict(p)
			if err == nil || len(m) != 0 {
				t.Errorf("%s: expected rejection, got %v %v", filepath.Base(p), m, err)
			}
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: readPlistDict blocked", filepath.Base(p))
		}
	}
}
