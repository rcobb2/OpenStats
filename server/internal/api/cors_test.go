package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCrossSiteGuard(t *testing.T) {
	h := crossSiteGuard([]string{"https://portal.example.edu"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	cases := []struct {
		name    string
		method  string
		headers map[string]string
		want    int
	}{
		{"no browser headers (agent/curl)", "POST", nil, 204},
		{"sec-fetch same-origin", "DELETE", map[string]string{"Sec-Fetch-Site": "same-origin"}, 204},
		{"sec-fetch none", "POST", map[string]string{"Sec-Fetch-Site": "none"}, 204},
		{"sec-fetch cross-site", "POST", map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
		{"sec-fetch same-site", "PUT", map[string]string{"Sec-Fetch-Site": "same-site"}, 403},
		{"sec-fetch cross-site allowed origin", "POST", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://portal.example.edu"}, 204},
		{"origin same host", "PATCH", map[string]string{"Origin": "http://openstats.test"}, 204},
		{"origin other host", "POST", map[string]string{"Origin": "https://evil.example"}, 403},
		{"origin null", "POST", map[string]string{"Origin": "null"}, 403},
		{"origin allowed", "POST", map[string]string{"Origin": "https://portal.example.edu"}, 204},
		{"GET cross-site passes", "GET", map[string]string{"Sec-Fetch-Site": "cross-site"}, 204},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, "http://openstats.test/api/v1/x", nil)
		for k, v := range c.headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s: status %d, want %d", c.name, rec.Code, c.want)
		}
	}
}
