package main

import (
	"testing"
)

// TestDecodeSocialConfigsResponse_RawShape pins the production bug: the
// /api/internal/oauth/social-providers handler uses jsonOK, which encodes
// SocialConfigs directly with NO {"data": ...} wrapper. Production payloads
// look exactly like the body string below — confirmed by curling the
// endpoint inside the muvee-server container on muveeai.com on 2026-05-21.
// authservice must decode that shape and surface admin-configured social
// providers; an earlier version assumed a wrapper, which silently ate them.
func TestDecodeSocialConfigsResponse_RawShape(t *testing.T) {
	body := []byte(`{"google":{"ClientID":"12312312312","ClientSecret":"123123123123","RedirectURL":""}}`)

	cfg, err := decodeSocialConfigsResponse(body)
	if err != nil {
		t.Fatalf("unexpected decode error: %v", err)
	}
	if cfg.Google == nil {
		t.Fatalf("expected Google config to be populated, got nil (response: %s)", body)
	}
	if cfg.Google.ClientID != "12312312312" {
		t.Errorf("expected Google.ClientID=12312312312, got %q", cfg.Google.ClientID)
	}
	if cfg.Google.ClientSecret != "123123123123" {
		t.Errorf("expected Google.ClientSecret=123123123123, got %q", cfg.Google.ClientSecret)
	}
}

// TestDecodeSocialConfigsResponse_EmptyBodyOK guards the no-providers-enabled
// case: muvee-server returns `{}` for SocialConfigs with all five pointers
// nil. authservice must accept that without erroring; it just means
// BuildSocialProviders builds nothing and reloadProviders falls back to the
// env-side platform providers alone.
func TestDecodeSocialConfigsResponse_EmptyBodyOK(t *testing.T) {
	cfg, err := decodeSocialConfigsResponse([]byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error on empty payload: %v", err)
	}
	if cfg.Google != nil || cfg.Discord != nil || cfg.Apple != nil || cfg.Facebook != nil || cfg.Twitter != nil {
		t.Errorf("expected all provider pointers nil, got %+v", cfg)
	}
}
