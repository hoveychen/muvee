package api

import "testing"

func TestNormaliseEnabledProviders_None(t *testing.T) {
	known := map[string]bool{"google": true, "feishu": true}

	// "none" (any case) is a valid sentinel meaning "no OAuth", canonicalised
	// to lowercase and NOT validated against known providers.
	for _, in := range []string{"none", "NONE", " None "} {
		got, err := normaliseEnabledProviders(in, known)
		if err != nil {
			t.Fatalf("normaliseEnabledProviders(%q) error: %v", in, err)
		}
		if got != EnabledProvidersNone {
			t.Errorf("normaliseEnabledProviders(%q) = %q, want %q", in, got, EnabledProvidersNone)
		}
	}

	// Empty still means inherit-all (distinct from none).
	if got, _ := normaliseEnabledProviders("", known); got != "" {
		t.Errorf("empty should stay empty (inherit all), got %q", got)
	}
	// A real provider list still validates.
	if got, err := normaliseEnabledProviders("feishu", known); err != nil || got != "feishu" {
		t.Errorf("feishu = %q err=%v", got, err)
	}
	// An unknown provider is still rejected.
	if _, err := normaliseEnabledProviders("nope", known); err == nil {
		t.Error("unknown provider should error")
	}
}
