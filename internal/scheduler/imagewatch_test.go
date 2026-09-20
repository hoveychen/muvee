package scheduler

import (
	"sort"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/hoveychen/muvee/internal/store"
)

func TestParseComposeImages(t *testing.T) {
	cases := []struct {
		name        string
		yaml        string
		wantImages  []string
		wantSkipped []string
	}{
		{
			name: "two literal images",
			yaml: `
services:
  web:
    image: nginx:1.25
    ports: ["80:80"]
  cache:
    image: redis:7-alpine
`,
			wantImages: []string{"nginx:1.25", "redis:7-alpine"},
		},
		{
			name: "interpolated image is skipped",
			yaml: `
services:
  app:
    image: ${REGISTRY}/myorg/myapp:${TAG}
  db:
    image: postgres:16
`,
			wantImages:  []string{"postgres:16"},
			wantSkipped: []string{"${REGISTRY}/myorg/myapp:${TAG}"},
		},
		{
			name: "bare $VAR is skipped",
			yaml: `
services:
  app:
    image: $REGISTRY/repo:tag
`,
			wantSkipped: []string{"$REGISTRY/repo:tag"},
		},
		{
			name: "escaped dollar is kept",
			yaml: `
services:
  app:
    image: literal:$$weird-but-valid
`,
			wantImages: []string{"literal:$$weird-but-valid"},
		},
		{
			name: "service without image field",
			yaml: `
services:
  built:
    build: .
  pulled:
    image: alpine:3.20
`,
			wantImages: []string{"alpine:3.20"},
		},
		{
			name: "duplicate image is collapsed",
			yaml: `
services:
  a:
    image: busybox:latest
  b:
    image: busybox:latest
`,
			wantImages: []string{"busybox:latest"},
		},
		{
			name:       "empty document",
			yaml:       ``,
			wantImages: nil,
		},
		{
			name: "invalid yaml returns nothing",
			yaml: `services: [this is not valid for our schema`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotImages, gotSkipped := parseComposeImages([]byte(tc.yaml))
			sort.Strings(gotImages)
			sort.Strings(gotSkipped)
			sort.Strings(tc.wantImages)
			sort.Strings(tc.wantSkipped)
			if !equalSlices(gotImages, tc.wantImages) {
				t.Errorf("images: got %v, want %v", gotImages, tc.wantImages)
			}
			if !equalSlices(gotSkipped, tc.wantSkipped) {
				t.Errorf("skipped: got %v, want %v", gotSkipped, tc.wantSkipped)
			}
		})
	}
}

func TestContainsInterpolation(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"nginx:1.25", false},
		{"registry.example.com/repo:tag", false},
		{"${VAR}", true},
		{"prefix${VAR}suffix", true},
		{"$VAR", true},
		{"${VAR:-default}", true},
		{"$$literal", false},     // escaped, no remaining $
		{"a$$b$VAR", true},       // $$ escapes, but $VAR survives
		{"sha256:$$digest", false}, // pure escape, no var
	}
	for _, tc := range cases {
		if got := containsInterpolation(tc.in); got != tc.want {
			t.Errorf("containsInterpolation(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestHostMatchesRegistryAddr(t *testing.T) {
	cases := []struct {
		name string
		ref  string
		cfg  string
		want bool
	}{
		{"exact host match", "registry.example.com", "registry.example.com", true},
		{"case insensitive", "Registry.Example.Com", "registry.example.com", true},
		{"both with port", "registry.example.com:5000", "registry.example.com:5000", true},
		{"ref without port, cfg with port", "registry.example.com", "registry.example.com:443", true},
		{"different host", "ghcr.io", "registry.example.com", false},
		{"empty ref", "", "registry.example.com", false},
		{"empty cfg", "registry.example.com", "", false},
		{"docker hub vs ours", "index.docker.io", "registry.example.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hostMatchesRegistryAddr(tc.ref, tc.cfg); got != tc.want {
				t.Errorf("hostMatchesRegistryAddr(%q, %q) = %v, want %v", tc.ref, tc.cfg, got, tc.want)
			}
		})
	}
}

func TestRegistryHostMatches(t *testing.T) {
	cases := []struct {
		name string
		ref  string
		addr string
		want bool
	}{
		{"exact", "ghcr.io", "ghcr.io", true},
		{"case insensitive", "GHCR.IO", "ghcr.io", true},
		{"addr carries a port", "harbor.corp.com", "harbor.corp.com:443", true},
		{"docker hub spellings fold together", "index.docker.io", "docker.io", true},
		{"registry-1 spelling too", "registry-1.docker.io", "docker.io", true},
		{"different registries", "ghcr.io", "docker.io", false},
		{"empty addr never matches", "ghcr.io", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := registryHostMatches(tc.ref, tc.addr); got != tc.want {
				t.Errorf("registryHostMatches(%q, %q) = %v, want %v", tc.ref, tc.addr, got, tc.want)
			}
		})
	}
}

// TestAuthForImage pins the priority order: our own registry wins, then a
// matching owner credential, then anonymous. The middle case is the whole
// point of the change — without it a private ghcr.io image can be deployed but
// never watched.
func TestAuthForImage(t *testing.T) {
	ownerAuths := []store.RegistryAuth{
		{Addr: "ghcr.io", Username: "gh-user", Password: "gh-token"},
		{Addr: "docker.io", Username: "dh-user", Password: "dh-token"},
		{Addr: "incomplete.io", Username: "", Password: "no-user"},
	}
	s := &Scheduler{
		registryAddr:     "registry.muvee.local:5000",
		registryUser:     "muvee",
		registryPassword: "muvee-pw",
	}

	cases := []struct {
		name         string
		image        string
		auths        []store.RegistryAuth
		wantUser     string // "" means anonymous
		wantPassword string
	}{
		{"our own registry wins", "registry.muvee.local:5000/app:latest", ownerAuths, "muvee", "muvee-pw"},
		{"private ghcr uses owner credential", "ghcr.io/org/repo:latest", ownerAuths, "gh-user", "gh-token"},
		{"docker hub shorthand normalises", "redis:7-alpine", ownerAuths, "dh-user", "dh-token"},
		{"unknown registry falls back to anonymous", "quay.io/org/repo:v1", ownerAuths, "", ""},
		{"credential missing a username is skipped", "incomplete.io/org/repo:v1", ownerAuths, "", ""},
		{"no credentials at all", "ghcr.io/org/repo:latest", nil, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref, err := name.ParseReference(tc.image)
			if err != nil {
				t.Fatalf("parse %q: %v", tc.image, err)
			}
			got := s.authForImage(ref, tc.auths)
			if tc.wantUser == "" {
				if got != authn.Anonymous {
					t.Fatalf("got %#v, want anonymous", got)
				}
				return
			}
			basic, ok := got.(*authn.Basic)
			if !ok {
				t.Fatalf("got %#v, want *authn.Basic", got)
			}
			if basic.Username != tc.wantUser || basic.Password != tc.wantPassword {
				t.Errorf("got %s/%s, want %s/%s", basic.Username, basic.Password, tc.wantUser, tc.wantPassword)
			}
		})
	}
}

func TestSameKeys(t *testing.T) {
	cases := []struct {
		name string
		a    map[string]string
		b    map[string]string
		want bool
	}{
		{"both empty", map[string]string{}, map[string]string{}, true},
		{"same keys diff values", map[string]string{"x": "1"}, map[string]string{"x": "2"}, true},
		{"different keys", map[string]string{"x": "1"}, map[string]string{"y": "1"}, false},
		{"different sizes", map[string]string{"x": "1", "y": "2"}, map[string]string{"x": "1"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameKeys(tc.a, tc.b); got != tc.want {
				t.Errorf("sameKeys = %v, want %v", got, tc.want)
			}
		})
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
