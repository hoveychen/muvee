package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(server string) *client {
	cfg := singleProfileConfig(strings.TrimRight(server, "/"), "test-token")
	return &client{
		cfg:    &cfg,
		server: strings.TrimRight(server, "/"),
		token:  "test-token",
	}
}

func TestResolveProjectRef_UUIDPassthrough(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		t.Errorf("unexpected API call to %s — UUID input must not hit the list endpoint", r.URL.Path)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	const id = "11111111-2222-3333-4444-555555555555"
	got, err := resolveProjectRef(c, id)
	if err != nil {
		t.Fatalf("resolveProjectRef(%q) error: %v", id, err)
	}
	if got != id {
		t.Errorf("resolveProjectRef(%q) = %q, want %q", id, got, id)
	}
	if called {
		t.Error("API was called even though arg was a valid UUID")
	}
}

func TestResolveProjectRef_NameSingleMatch(t *testing.T) {
	const wantID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/projects" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{"id":"` + wantID + `","name":"my-app"},
			{"id":"00000000-0000-0000-0000-000000000001","name":"other-app"}
		]`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	got, err := resolveProjectRef(c, "my-app")
	if err != nil {
		t.Fatalf("resolveProjectRef(\"my-app\") error: %v", err)
	}
	if got != wantID {
		t.Errorf("resolveProjectRef(\"my-app\") = %q, want %q", got, wantID)
	}
}

func TestResolveProjectRef_NameNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"id":"00000000-0000-0000-0000-000000000001","name":"other-app"}]`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, err := resolveProjectRef(c, "missing-app")
	if err == nil {
		t.Fatal("expected error for missing project name, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error %q should mention 'not found'", err.Error())
	}
	if !strings.Contains(err.Error(), "missing-app") {
		t.Errorf("error %q should include the queried name", err.Error())
	}
}

func TestResolveDatasetRef_UUIDPassthrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected API call to %s — UUID input must not hit the list endpoint", r.URL.Path)
	}))
	defer srv.Close()
	c := newTestClient(srv.URL)
	const id = "33333333-4444-5555-6666-777777777777"
	got, err := resolveDatasetRef(c, id)
	if err != nil {
		t.Fatalf("resolveDatasetRef(%q) error: %v", id, err)
	}
	if got != id {
		t.Errorf("resolveDatasetRef(%q) = %q, want %q", id, got, id)
	}
}

func TestResolveDatasetRef_NameSingleMatch(t *testing.T) {
	const wantID = "cccccccc-dddd-eeee-ffff-000000000001"
	var hitPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"id":"` + wantID + `","name":"my-dataset"}]`))
	}))
	defer srv.Close()
	c := newTestClient(srv.URL)
	got, err := resolveDatasetRef(c, "my-dataset")
	if err != nil {
		t.Fatalf("resolveDatasetRef error: %v", err)
	}
	if got != wantID {
		t.Errorf("resolveDatasetRef = %q, want %q", got, wantID)
	}
	if hitPath != "/api/datasets" {
		t.Errorf("hit path = %q, want /api/datasets", hitPath)
	}
}

func TestResolveDatasetRef_NameNotFoundMentionsKind(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	c := newTestClient(srv.URL)
	_, err := resolveDatasetRef(c, "missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "dataset") {
		t.Errorf("error %q should mention 'dataset' (so user knows which resource was missing)", err.Error())
	}
}

func TestResolveProjectRef_NameAmbiguous(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{"id":"00000000-0000-0000-0000-000000000001","name":"dup"},
			{"id":"00000000-0000-0000-0000-000000000002","name":"dup"}
		]`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, err := resolveProjectRef(c, "dup")
	if err == nil {
		t.Fatal("expected error for ambiguous project name, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "ambiguous") {
		t.Errorf("error %q should mention 'ambiguous'", err.Error())
	}
}
