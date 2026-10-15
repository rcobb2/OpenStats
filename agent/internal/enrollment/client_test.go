package enrollment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// The exhaustive normalization table lives in internal/urlutil. This just
// confirms enrollment's entry point is actually wired to it.
func TestNormalizeServerURLDelegates(t *testing.T) {
	if got := NormalizeServerURL("openstats.colgate.edu"); got != "https://openstats.colgate.edu" {
		t.Errorf("NormalizeServerURL did not normalize: got %q", got)
	}
	if got := NormalizeServerURL(""); got != "" {
		t.Errorf("empty input should stay empty, got %q", got)
	}
}

// NewClient must normalize, or every request built from serverURL fails with
// `unsupported protocol scheme ""`.
func TestNewClientNormalizesServerURL(t *testing.T) {
	c := NewClient("openstats.colgate.edu/", 9183, "", "", testLogger())
	if c.serverURL != "https://openstats.colgate.edu" {
		t.Errorf("NewClient stored %q, want https://openstats.colgate.edu", c.serverURL)
	}
}

// A scheme-less address used to make isTrustedUpdateHost compare against an
// empty hostname, so every server-directed self-update was silently rejected.
func TestTrustedUpdateHostWorksForSchemelessConfig(t *testing.T) {
	c := NewClient("openstats.colgate.edu", 9183, "", "", testLogger())
	if !c.isTrustedUpdateHost("openstats.colgate.edu") {
		t.Errorf("expected openstats.colgate.edu to be trusted, serverURL=%q", c.serverURL)
	}
	if c.isTrustedUpdateHost("evil.example.com") {
		t.Error("unrelated host must not be trusted")
	}
}

func TestVerifyDownloadChecksumEmptyExpectedAlwaysPasses(t *testing.T) {
	path := t.TempDir() + "/file.bin"
	if err := os.WriteFile(path, []byte("anything"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyDownloadChecksum(path, ""); err != nil {
		t.Errorf("expected nil error for empty expected checksum, got %v", err)
	}
}

func TestVerifyDownloadChecksumMatches(t *testing.T) {
	content := []byte("totally real msi bytes")
	path := t.TempDir() + "/file.bin"
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	expected := hex.EncodeToString(sum[:])

	if err := verifyDownloadChecksum(path, expected); err != nil {
		t.Errorf("expected nil error for matching checksum, got %v", err)
	}
	// Case-insensitive comparison (server hex encoding should always be
	// lowercase, but don't be brittle about it).
	if err := verifyDownloadChecksum(path, strings.ToUpper(expected)); err != nil {
		t.Errorf("expected case-insensitive match to pass, got %v", err)
	}
}

// Regression: RunHeartbeat used to call IsInMaintenanceWindow directly, with
// no way for setmaintenance's override to ever reach this decision — the CLI
// command printed a convincing "forced on/off" message while the running
// service kept gating self-updates purely on the server's time-based window.
// inMaintenanceWindow must let a persisted override win outright in both
// directions, and fall through to the time-based window when none is set.
func TestInMaintenanceWindowOverrideWinsOverSchedule(t *testing.T) {
	dir := t.TempDir()
	c := NewClient("openstats.colgate.edu", 9183, "", "", testLogger()).WithMaintenanceOverrideDir(dir)

	// Outside any configured window by the time-based check (start==end means
	// never), so a nil override must say "not in window".
	neverSettings := &SystemSettings{MaintenanceWindowStart: "10:00", MaintenanceWindowEnd: "10:00"}
	if c.inMaintenanceWindow(neverSettings) {
		t.Fatal("expected false with no override and a zero-length window")
	}

	on := true
	if err := WriteMaintenanceOverride(dir, &on); err != nil {
		t.Fatal(err)
	}
	if !c.inMaintenanceWindow(neverSettings) {
		t.Error("forced-on override should win even when the schedule says never")
	}

	off := false
	if err := WriteMaintenanceOverride(dir, &off); err != nil {
		t.Fatal(err)
	}
	alwaysSettings := &SystemSettings{} // empty start/end means "always in window"
	if c.inMaintenanceWindow(alwaysSettings) {
		t.Error("forced-off override should win even when the schedule says always")
	}
}

func TestVerifyDownloadChecksumMismatchErrors(t *testing.T) {
	path := t.TempDir() + "/file.bin"
	if err := os.WriteFile(path, []byte("real content"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := verifyDownloadChecksum(path, "0000000000000000000000000000000000000000000000000000000000000000")
	if err == nil {
		t.Fatal("expected an error for a checksum mismatch")
	}
}

// idleThresholdSeconds must decode to nil when absent (older server: keep the
// local config), and 0 must stay distinguishable from absent (disabled).
func TestRegisterDecodesIdleThresholdAndInvokesHandler(t *testing.T) {
	cases := []struct {
		name string
		json string
		want *int
	}{
		{"absent", `{"settings":{"heartbeatIntervalSeconds":60}}`, nil},
		{"zero", `{"settings":{"idleThresholdSeconds":0}}`, intPtr(0)},
		{"600", `{"settings":{"idleThresholdSeconds":600}}`, intPtr(600)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, tc.json)
			}))
			defer srv.Close()

			var got *SystemSettings
			calls := 0
			c := NewClient(srv.URL, 9183, "", "", testLogger()).
				WithSettingsHandler(func(s *SystemSettings) { got = s; calls++ })
			c.Register(context.Background())

			if calls != 1 || got == nil {
				t.Fatalf("handler calls=%d settings=%v, want 1 call with settings", calls, got)
			}
			switch {
			case tc.want == nil && got.IdleThresholdSeconds != nil:
				t.Errorf("want nil, got %d", *got.IdleThresholdSeconds)
			case tc.want != nil && (got.IdleThresholdSeconds == nil || *got.IdleThresholdSeconds != *tc.want):
				t.Errorf("want %d, got %v", *tc.want, got.IdleThresholdSeconds)
			}
		})
	}
}

func intPtr(v int) *int { return &v }

// pushHarness stands up a fake local /metrics endpoint and a fake server and
// returns a client wired to both, plus the ordered event log the hooks and
// handlers append to.
func pushHarness(t *testing.T, serverStatus int, serverDown bool) (*Client, *[]string) {
	t.Helper()
	var events []string
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		events = append(events, "get")
		w.Write([]byte("m 0\n"))
	}))
	t.Cleanup(local.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		events = append(events, "post")
		w.WriteHeader(serverStatus)
	}))
	t.Cleanup(srv.Close)
	serverURL := srv.URL
	if serverDown {
		srv.Close()
	}
	u, _ := url.Parse(local.URL)
	port, _ := strconv.Atoi(u.Port())
	c := NewClient(serverURL, port, "", "", testLogger()).
		WithMetricsDeliveryHooks(
			func() uint64 { events = append(events, "begin"); return 42 },
			func(tok uint64) { events = append(events, "delivered:"+strconv.FormatUint(tok, 10)) },
		)
	return c, &events
}

func TestPushMetricsDeliveryHooksOnSuccess(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusOK} {
		c, events := pushHarness(t, status, false)
		if err := c.PushMetrics(context.Background()); err != nil {
			t.Fatalf("status %d: %v", status, err)
		}
		if got := strings.Join(*events, ","); got != "begin,get,post,delivered:42" {
			t.Errorf("status %d: events = %s", status, got)
		}
	}
}

func TestPushMetricsNoDeliveryOnFailure(t *testing.T) {
	c, events := pushHarness(t, http.StatusInternalServerError, false)
	if err := c.PushMetrics(context.Background()); err == nil {
		t.Fatal("expected error on non-2xx")
	}
	if got := strings.Join(*events, ","); got != "begin,get,post" {
		t.Errorf("non-2xx events = %s (delivered must not fire)", got)
	}

	c, events = pushHarness(t, http.StatusNoContent, true)
	if err := c.PushMetrics(context.Background()); err == nil {
		t.Fatal("expected error when POST fails")
	}
	if got := strings.Join(*events, ","); got != "begin,get" {
		t.Errorf("POST error events = %s (delivered must not fire)", got)
	}
}
