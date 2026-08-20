package api

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// jpegDataURI builds the avatar shape internal/auth/entra.go produces from a
// Microsoft Graph photo.
func jpegDataURI(payload []byte) string {
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(payload)
}

func avatarTestServer(t *testing.T, baseDomain string) *Server {
	t.Helper()
	return &Server{brandingDir: t.TempDir(), baseDomain: baseDomain}
}

func TestMaterializeAvatarStoresDataURIAndReturnsURL(t *testing.T) {
	s := avatarTestServer(t, "muveeai.com")

	// An 8 KB photo: the size that produced a 14.9 KB session cookie before.
	got := s.MaterializeAvatar(jpegDataURI(make([]byte, 8<<10)))

	if !strings.HasPrefix(got, "https://muveeai.com/api/public/avatars/avatar-") {
		t.Fatalf("avatar URL = %q, want an absolute /api/public/avatars/ URL", got)
	}
	if !strings.HasSuffix(got, ".jpg") {
		t.Errorf("avatar URL = %q, want a .jpg extension", got)
	}
	// The whole point: what now travels into a cookie is a short URL.
	if len(got) > 128 {
		t.Errorf("avatar URL is %d bytes; expected something small enough for a cookie", len(got))
	}

	name := avatarFilename(got)
	if _, err := os.Stat(filepath.Join(s.avatarDir(), name)); err != nil {
		t.Errorf("avatar file %s was not written: %v", name, err)
	}
}

// avatarFilename returns the trailing filename of a materialised avatar URL.
func avatarFilename(url string) string {
	i := strings.LastIndexByte(url, '/')
	return url[i+1:]
}

func TestMaterializeAvatarPassesThroughProviderURLs(t *testing.T) {
	s := avatarTestServer(t, "muveeai.com")

	// Google's `picture` and Lark's `avatar_url` must survive byte-for-byte.
	for _, in := range []string{
		"https://lh3.googleusercontent.com/a/ACg8ocKQ=s96-c",
		"https://s1-imfile.feishucdn.com/static-resource/v1/abc~",
		"",
	} {
		if got := s.MaterializeAvatar(in); got != in {
			t.Errorf("MaterializeAvatar(%q) = %q, want it unchanged", in, got)
		}
	}
}

// TestMaterializeAvatarDeduplicatesByContent pins the content-hash naming: a
// user signing in repeatedly must not pile up copies of the same photo.
func TestMaterializeAvatarDeduplicatesByContent(t *testing.T) {
	s := avatarTestServer(t, "muveeai.com")
	uri := jpegDataURI([]byte("the-same-photo-bytes"))

	first := s.MaterializeAvatar(uri)
	second := s.MaterializeAvatar(uri)
	if first != second {
		t.Errorf("same photo produced two URLs: %q vs %q", first, second)
	}
	entries, err := os.ReadDir(s.avatarDir())
	if err != nil {
		t.Fatalf("read avatar dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("wrote %d files for one photo, want 1", len(entries))
	}
}

// TestMaterializeAvatarNeverFailsLogin: a value it cannot store comes back
// unchanged rather than empty, so the sign-in still completes.
func TestMaterializeAvatarNeverFailsLogin(t *testing.T) {
	s := avatarTestServer(t, "muveeai.com")

	cases := map[string]string{
		"unsupported mime": "data:image/tiff;base64," + base64.StdEncoding.EncodeToString([]byte("x")),
		"not base64":       "data:image/jpeg,rawbytes",
		"no comma":         "data:image/jpeg;base64",
		"oversized":        jpegDataURI(make([]byte, maxAvatarBytes+1)),
		"empty payload":    jpegDataURI(nil),
	}
	for name, in := range cases {
		if got := s.MaterializeAvatar(in); got != in {
			t.Errorf("%s: MaterializeAvatar returned %q, want the input echoed back", name, got)
		}
	}
}

// TestMaterializeAvatarWithoutStorageIsANoop covers a server started with no
// BRANDING_DIR: materialisation is skipped, the data URI passes through, and
// the cookie guard in authservice is what protects the session.
func TestMaterializeAvatarWithoutStorageIsANoop(t *testing.T) {
	s := &Server{baseDomain: "muveeai.com"}
	uri := jpegDataURI(make([]byte, 1<<10))
	if got := s.MaterializeAvatar(uri); got != uri {
		t.Errorf("with no storage configured, want the input echoed back")
	}
}

// TestMaterializeAvatarRelativeWithoutBaseDomain is the local-development
// shape: no BASE_DOMAIN, so the URL is root-relative.
func TestMaterializeAvatarRelativeWithoutBaseDomain(t *testing.T) {
	s := avatarTestServer(t, "")
	got := s.MaterializeAvatar(jpegDataURI([]byte("dev")))
	if !strings.HasPrefix(got, "/api/public/avatars/avatar-") {
		t.Errorf("avatar URL = %q, want a root-relative path", got)
	}
}

func TestHandleServeAvatar(t *testing.T) {
	s := avatarTestServer(t, "muveeai.com")
	url := s.MaterializeAvatar(jpegDataURI([]byte("photo-bytes")))
	filename := avatarFilename(url)

	r := chi.NewRouter()
	r.Get("/api/public/avatars/{filename}", s.handleServeAvatar)

	t.Run("serves the stored file", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/public/avatars/"+filename, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if body := rec.Body.String(); body != "photo-bytes" {
			t.Errorf("body = %q, want the stored bytes", body)
		}
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
			t.Errorf("Cache-Control = %q, want an immutable cache directive", cc)
		}
	})

	t.Run("404s an unknown file", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/public/avatars/avatar-deadbeef.jpg", nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})

	// Path traversal: write a file outside the avatar dir and try to reach it.
	t.Run("rejects traversal", func(t *testing.T) {
		secret := filepath.Join(s.brandingDir, "secret.txt")
		if err := os.WriteFile(secret, []byte("nope"), 0o644); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/public/avatars/x", nil)
		// Bypass the router's own path cleaning by injecting the param directly.
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("filename", "../secret.txt")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		s.handleServeAvatar(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404 for a traversal attempt", rec.Code)
		}
	})
}
