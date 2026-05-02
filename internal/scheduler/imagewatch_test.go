package scheduler

import (
	"sort"
	"testing"
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
