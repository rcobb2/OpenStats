package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// PUT /mappings and PUT /users/mappings are full-row upserts server-side (ON
// CONFLICT DO UPDATE SET every column), not partial patches. These tests guard
// against regressing back to sending a flag's absence as an explicit blank/
// false, which would silently wipe fields the caller never touched.

func TestMappingsSetMergesOntoExistingMapping(t *testing.T) {
	var putBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/mappings":
			json.NewEncoder(w).Encode([]softwareMapping{
				{ID: 1, ExeName: "chrome.exe", DisplayName: "Chrome", Category: "Browser", Publisher: "Google", Family: "chrome", Source: "manual", Ignored: true},
			})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/mappings":
			json.NewDecoder(r.Body).Decode(&putBody)
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	c := newClient(srv.URL)
	// Only renaming — category/publisher/family/ignored must survive untouched.
	if err := mappingsSet(c, []string{"chrome.exe", "--name", "Google Chrome"}); err != nil {
		t.Fatalf("mappingsSet: %v", err)
	}

	if putBody["displayName"] != "Google Chrome" {
		t.Errorf("displayName = %v, want %q", putBody["displayName"], "Google Chrome")
	}
	if putBody["category"] != "Browser" {
		t.Errorf("category = %v, want %q (should be preserved from existing mapping)", putBody["category"], "Browser")
	}
	if putBody["publisher"] != "Google" {
		t.Errorf("publisher = %v, want %q (should be preserved)", putBody["publisher"], "Google")
	}
	if putBody["family"] != "chrome" {
		t.Errorf("family = %v, want %q (should be preserved)", putBody["family"], "chrome")
	}
	if putBody["ignored"] != true {
		t.Errorf("ignored = %v, want true (must not be silently un-ignored)", putBody["ignored"])
	}
}

func TestMappingsSetRequiresNameForNewMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode([]softwareMapping{})
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	c := newClient(srv.URL)
	if err := mappingsSet(c, []string{"new.exe"}); err == nil {
		t.Fatal("expected an error requiring --name for a brand-new mapping")
	}
}

func TestUsersAliasMergesOntoExistingRule(t *testing.T) {
	var putBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/users/mappings":
			json.NewEncoder(w).Encode([]userMapping{
				{ID: 1, Pattern: "jdoe-old", CanonicalUser: "jdoe", DisplayName: "John Doe", Notes: "set up during onboarding", Source: "manual", Ignored: true},
			})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/users/mappings":
			json.NewDecoder(r.Body).Decode(&putBody)
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	c := newClient(srv.URL)
	if err := cmdUsers(c, []string{"alias", "jdoe-old", "jdoe2"}); err != nil {
		t.Fatalf("cmdUsers alias: %v", err)
	}

	if putBody["canonicalUser"] != "jdoe2" {
		t.Errorf("canonicalUser = %v, want %q", putBody["canonicalUser"], "jdoe2")
	}
	if putBody["displayName"] != "John Doe" {
		t.Errorf("displayName = %v, want %q (should be preserved)", putBody["displayName"], "John Doe")
	}
	if putBody["notes"] != "set up during onboarding" {
		t.Errorf("notes = %v, want preserved existing note, got overwritten", putBody["notes"])
	}
	if putBody["ignored"] != true {
		t.Errorf("ignored = %v, want true (must not be silently un-ignored)", putBody["ignored"])
	}
}
