package deployer

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func decodeAuths(t *testing.T, b []byte) map[string]interface{} {
	t.Helper()
	var cfg map[string]interface{}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	auths, ok := cfg["auths"].(map[string]interface{})
	if !ok {
		t.Fatalf("output has no auths map: %s", b)
	}
	return auths
}

func authToken(t *testing.T, auths map[string]interface{}, addr string) string {
	t.Helper()
	entry, ok := auths[addr].(map[string]interface{})
	if !ok {
		t.Fatalf("no auth entry for %q in %v", addr, auths)
	}
	tok, _ := entry["auth"].(string)
	return tok
}

func TestBuildDockerConfigJSON_EmptyBase(t *testing.T) {
	out, err := buildDockerConfigJSON(nil, []RegistryAuth{
		{Addr: "ghcr.io", Username: "alice", Password: "secret"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	auths := decodeAuths(t, out)
	want := base64.StdEncoding.EncodeToString([]byte("alice:secret"))
	if got := authToken(t, auths, "ghcr.io"); got != want {
		t.Errorf("ghcr.io auth = %q, want %q", got, want)
	}
}

func TestBuildDockerConfigJSON_MergesAndPreservesBase(t *testing.T) {
	base := []byte(`{"auths":{"registry.muveeai.com":{"auth":"ZXhpc3Rpbmc="}},"credsStore":"desktop"}`)
	out, err := buildDockerConfigJSON(base, []RegistryAuth{
		{Addr: "ghcr.io", Username: "bob", Password: "pw"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	auths := decodeAuths(t, out)
	// Existing primary-registry auth preserved.
	if got := authToken(t, auths, "registry.muveeai.com"); got != "ZXhpc3Rpbmc=" {
		t.Errorf("primary auth not preserved: %q", got)
	}
	// New ghcr auth added.
	want := base64.StdEncoding.EncodeToString([]byte("bob:pw"))
	if got := authToken(t, auths, "ghcr.io"); got != want {
		t.Errorf("ghcr.io auth = %q, want %q", got, want)
	}
	// Unrelated top-level keys preserved.
	var cfg map[string]interface{}
	_ = json.Unmarshal(out, &cfg)
	if cfg["credsStore"] != "desktop" {
		t.Errorf("credsStore not preserved: %v", cfg["credsStore"])
	}
}

func TestBuildDockerConfigJSON_OverwritesSameAddr(t *testing.T) {
	base := []byte(`{"auths":{"ghcr.io":{"auth":"b2xk"}}}`)
	out, err := buildDockerConfigJSON(base, []RegistryAuth{
		{Addr: "ghcr.io", Username: "new", Password: "creds"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	auths := decodeAuths(t, out)
	want := base64.StdEncoding.EncodeToString([]byte("new:creds"))
	if got := authToken(t, auths, "ghcr.io"); got != want {
		t.Errorf("ghcr.io auth = %q, want %q (should overwrite)", got, want)
	}
}

func TestPrepareRegistryDockerConfig_NoAuthsIsNoop(t *testing.T) {
	dir, cleanup, err := prepareRegistryDockerConfig(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()
	if dir != "" {
		t.Errorf("dir = %q, want empty for no auths", dir)
	}
}
