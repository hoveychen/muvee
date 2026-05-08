package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestParseProjectID_Valid(t *testing.T) {
	const want = "11111111-2222-3333-4444-555555555555"
	got, err := parseProjectID(want)
	if err != nil {
		t.Fatalf("parseProjectID(%q) error: %v", want, err)
	}
	if got == uuid.Nil {
		t.Errorf("parseProjectID(%q) returned nil UUID", want)
	}
	if got.String() != want {
		t.Errorf("parseProjectID(%q) = %s, want %s", want, got, want)
	}
}

func TestParseProjectID_InvalidReturnsError(t *testing.T) {
	_, err := parseProjectID("not-a-uuid")
	if err == nil {
		t.Fatal("parseProjectID(\"not-a-uuid\") expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("error %q should mention 'invalid'", err.Error())
	}
}

func TestParseProjectID_EmptyReturnsError(t *testing.T) {
	_, err := parseProjectID("")
	if err == nil {
		t.Fatal("parseProjectID(\"\") expected error, got nil")
	}
}

// reqWithChiParam returns a request that chi.URLParam(r, name) will resolve.
func reqWithChiParam(method, target, name, value string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(name, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// TestListDeployments_InvalidUUIDReturns400 reproduces the original UX bug:
// passing a project name (or any non-UUID) to a handler that expects a UUID
// path param used to silently coerce to the zero UUID, returning a 200 with
// an empty array — which the CLI rendered as "No deployments found." Now it
// must return 400 invalid id, before any store call.
func TestListDeployments_InvalidUUIDReturns400(t *testing.T) {
	s := &Server{} // store deliberately nil — handler must reject before touching it.
	r := reqWithChiParam(http.MethodGet, "/api/projects/my-app/deployments", "id", "my-app")
	w := httptest.NewRecorder()
	s.listDeployments(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (body=%q)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid") {
		t.Errorf("body %q should mention 'invalid'", w.Body.String())
	}
}

func TestGetProject_InvalidUUIDReturns400(t *testing.T) {
	s := &Server{}
	r := reqWithChiParam(http.MethodGet, "/api/projects/my-app", "id", "my-app")
	w := httptest.NewRecorder()
	s.getProject(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (body=%q)", w.Code, w.Body.String())
	}
}
