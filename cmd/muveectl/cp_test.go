package main

import "testing"

func TestParseCpRef(t *testing.T) {
	cases := []struct {
		in         string
		wantRef    string
		wantPath   string
		wantRemote bool
	}{
		{"./local", "", "./local", false},
		{"/absolute/path", "", "/absolute/path", false},
		{"my-project:/app/config.json", "my-project", "/app/config.json", true},
		{"abcd-uuid:/tmp/file", "abcd-uuid", "/tmp/file", true},
		// Empty leading segment is treated as local (no project ref).
		{":weird", "", ":weird", false},
		// Bare colon with nothing before is local.
		{"./file:with:colons", "./file", "with:colons", true}, // first colon wins; documented limitation
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			ref, path, remote := parseCpRef(tc.in)
			if ref != tc.wantRef || path != tc.wantPath || remote != tc.wantRemote {
				t.Fatalf("parseCpRef(%q) = (%q, %q, %v); want (%q, %q, %v)",
					tc.in, ref, path, remote, tc.wantRef, tc.wantPath, tc.wantRemote)
			}
		})
	}
}
