package auth

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newAvatarTestProvider(t *testing.T, graphBase string, fetchAvatar bool) *entraProvider {
	t.Helper()
	p, err := newEntraProvider(EntraConfig{
		TenantID:     testTenantGUID,
		ClientID:     "cid",
		ClientSecret: "sec",
		FetchAvatar:  fetchAvatar,
	}, "https://example.com/cb")
	if err != nil || p == nil {
		t.Fatalf("newEntraProvider: p=%v err=%v", p, err)
	}
	p.graphBaseURL = graphBase
	return p
}

// The Graph scope must only be requested when avatars are on — asking for it
// unconditionally would break sign-in in a tenant that withholds consent.
func TestEntraScopes(t *testing.T) {
	if got := strings.Join(entraScopes(false), " "); got != "openid profile email" {
		t.Errorf("scopes without avatar = %q", got)
	}
	if got := strings.Join(entraScopes(true), " "); got != "openid profile email User.Read" {
		t.Errorf("scopes with avatar = %q", got)
	}

	on := newAvatarTestProvider(t, "https://graph.invalid", true)
	if !on.avatarEnabled() {
		t.Error("avatarEnabled() should be true when FetchAvatar is set")
	}
	if !strings.Contains(on.AuthCodeURL("s", ""), "User.Read") {
		t.Error("authorize URL should carry the Graph scope when avatars are on")
	}
	off := newAvatarTestProvider(t, "https://graph.invalid", false)
	if off.avatarEnabled() {
		t.Error("avatarEnabled() should be false when FetchAvatar is unset")
	}
	if strings.Contains(off.AuthCodeURL("s", ""), "User.Read") {
		t.Error("authorize URL must not carry the Graph scope when avatars are off")
	}
}

func TestFetchAvatarReturnsDataURI(t *testing.T) {
	photo := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'}
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(photo)
	}))
	defer srv.Close()

	p := newAvatarTestProvider(t, srv.URL, true)
	got, err := p.fetchAvatar(context.Background(), "tok-123")
	if err != nil {
		t.Fatalf("fetchAvatar: %v", err)
	}
	want := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(photo)
	if got != want {
		t.Errorf("data URI = %q, want %q", got, want)
	}
	if gotPath != "/me/photos/96x96/$value" {
		t.Errorf("graph path = %q", gotPath)
	}
	if gotAuth != "Bearer tok-123" {
		t.Errorf("auth header = %q", gotAuth)
	}
}

// A user with no photo is the common case, not an error: Graph answers 404 and
// the login must still succeed with an empty avatar.
func TestFetchAvatarNoPhotoIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	got, err := newAvatarTestProvider(t, srv.URL, true).fetchAvatar(context.Background(), "tok")
	if err != nil {
		t.Fatalf("404 should not be an error, got %v", err)
	}
	if got != "" {
		t.Errorf("avatar = %q, want empty", got)
	}
}

func TestFetchAvatarErrors(t *testing.T) {
	t.Run("graph failure surfaces", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "forbidden", http.StatusForbidden)
		}))
		defer srv.Close()
		if _, err := newAvatarTestProvider(t, srv.URL, true).fetchAvatar(context.Background(), "tok"); err == nil {
			t.Error("expected an error for a 403 from Graph")
		}
	})

	t.Run("missing access token", func(t *testing.T) {
		if _, err := newAvatarTestProvider(t, "https://graph.invalid", true).fetchAvatar(context.Background(), " "); err == nil {
			t.Error("expected an error when the token response carried no access token")
		}
	})

	// An oversized payload is rejected rather than inlined into the user row.
	t.Run("oversized photo rejected", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(make([]byte, (256<<10)+1))
		}))
		defer srv.Close()
		if _, err := newAvatarTestProvider(t, srv.URL, true).fetchAvatar(context.Background(), "tok"); err == nil {
			t.Error("expected an error for an oversized photo")
		}
	})

	// Graph returns an empty body for some tenants instead of 404.
	t.Run("empty body means no photo", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/jpeg")
		}))
		defer srv.Close()
		got, err := newAvatarTestProvider(t, srv.URL, true).fetchAvatar(context.Background(), "tok")
		if err != nil || got != "" {
			t.Errorf("got %q, err %v; want empty, nil", got, err)
		}
	})
}

// EntraConfigFromSettings must resolve the avatar toggle so both planes agree,
// defaulting to ON when the admin has never touched the setting.
func TestEntraConfigFromSettingsAvatarToggle(t *testing.T) {
	t.Setenv("ENTRA_FETCH_AVATAR", "")
	if !EntraConfigFromSettings(map[string]string{}).FetchAvatar {
		t.Error("avatars should default to on")
	}
	if EntraConfigFromSettings(map[string]string{"entra_avatar_enabled": "false"}).FetchAvatar {
		t.Error("entra_avatar_enabled=false must disable the Graph call")
	}
	if !EntraConfigFromSettings(map[string]string{"entra_avatar_enabled": "true"}).FetchAvatar {
		t.Error("entra_avatar_enabled=true must enable the Graph call")
	}
}
