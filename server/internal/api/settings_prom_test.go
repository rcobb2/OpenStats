package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidHHMM(t *testing.T) {
	for in, want := range map[string]bool{
		"22:00": true, "00:00": true, "23:59": true, "4:05": true,
		"": false, "24:00": false, "12:60": false, "abc": false, "12": false, "-1:00": false,
	} {
		if got := validHHMM(in); got != want {
			t.Errorf("validHHMM(%q) = %v, want %v", in, got, want)
		}
	}
}

// Invalid payloads are rejected before the store is touched, so a Server with
// no store is enough to exercise validation.
func TestUpdateSettingsValidation(t *testing.T) {
	s := &Server{logger: slog.Default()}
	base := `"staleTimeoutDays":30,"rolloutMaxConcurrent":5`
	cases := map[string]string{
		"grace zero":       `{` + base + `,"rolloutGraceSeconds":0}`,
		"grace too small":  `{` + base + `,"rolloutGraceSeconds":30}`,
		"bad window":       `{` + base + `,"rolloutGraceSeconds":60,"maintenanceWindowStart":"nope","maintenanceWindowEnd":"04:00"}`,
		"half window":      `{` + base + `,"rolloutGraceSeconds":60,"maintenanceWindowStart":"22:00"}`,
		"out of range end": `{` + base + `,"rolloutGraceSeconds":60,"maintenanceWindowStart":"22:00","maintenanceWindowEnd":"25:00"}`,
	}
	for name, body := range cases {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
		rec := httptest.NewRecorder()
		s.UpdateSettings(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

func TestPromGetJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ok" {
			w.Write([]byte(`{"status":"success"}`))
			return
		}
		http.Error(w, strings.Repeat("x", 2000), http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	s := &Server{promClient: &http.Client{Timeout: 5 * time.Second}}

	var out struct{ Status string }
	if err := s.promGetJSON(context.Background(), srv.URL+"/ok", &out); err != nil || out.Status != "success" {
		t.Fatalf("ok path: err=%v out=%+v", err, out)
	}
	err := s.promGetJSON(context.Background(), srv.URL+"/bad", &out)
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("expected 503 error, got %v", err)
	}
	if len(err.Error()) > 600 {
		t.Errorf("error body not truncated: %d bytes", len(err.Error()))
	}
}
