package installersync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// syncOnce must download new .pkg/.msi assets, skip non-installer assets, skip
// files already present, and never clobber an existing installer.
func TestSyncOnceDownloadsNewInstallers(t *testing.T) {
	publicDir := t.TempDir()
	instDir := filepath.Join(publicDir, "installers")
	if err := os.MkdirAll(instDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pretend this one is already present — it must be left untouched.
	existing := filepath.Join(instDir, "openlabstats-agent-0.4.0.msi")
	if err := os.WriteFile(existing, []byte("ORIGINAL"), 0o644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/app/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		json.NewEncoder(w).Encode(release{
			TagName: "v0.4.0",
			Assets: []releaseAsset{
				{Name: "openlabstats-agent-0.4.0-universal.pkg", URL: base + "/dl/pkg"},
				{Name: "openlabstats-agent-0.4.0.msi", URL: base + "/dl/msi"}, // already present → skip
				{Name: "checksums.txt", URL: base + "/dl/txt"},                // not an installer → skip
			},
		})
	})
	mux.HandleFunc("/dl/pkg", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "PKGDATA") })
	mux.HandleFunc("/dl/msi", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "SHOULD-NOT-BE-FETCHED") })
	mux.HandleFunc("/dl/txt", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "nope") })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	old := githubAPIBase
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = old }()

	if err := syncOnce(context.Background(), srv.Client(), publicDir, "acme/app", slog.Default()); err != nil {
		t.Fatalf("syncOnce: %v", err)
	}

	// New .pkg downloaded.
	pkg := filepath.Join(instDir, "openlabstats-agent-0.4.0-universal.pkg")
	if b, err := os.ReadFile(pkg); err != nil || string(b) != "PKGDATA" {
		t.Errorf("expected downloaded pkg with PKGDATA, got %q err=%v", string(b), err)
	}
	// Existing .msi left untouched (not re-downloaded).
	if b, err := os.ReadFile(existing); err != nil || string(b) != "ORIGINAL" {
		t.Errorf("existing installer should be preserved, got %q err=%v", string(b), err)
	}
	// Non-installer asset not written.
	if _, err := os.Stat(filepath.Join(instDir, "checksums.txt")); !os.IsNotExist(err) {
		t.Error("non-installer asset should have been skipped")
	}
	// No leftover temp files.
	entries, _ := os.ReadDir(instDir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" || len(e.Name()) > 0 && e.Name()[0] == '.' {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}
