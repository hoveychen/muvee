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

// TestCpUploadTarget covers the kubectl/scp-style destination semantics for
// `muveectl projects cp` uploads. The reported bug: uploading a local file to a
// file destination (e.g. proj:/dir/name.txt) failed with "must be a directory"
// because the tar entry kept the local basename and the container extraction
// path was the file path itself. A single regular file with a non-directory
// destination must be renamed to the destination's last component and extracted
// into the parent directory.
func TestCpUploadTarget(t *testing.T) {
	cases := []struct {
		name         string
		srcIsRegular bool
		remotePath   string
		wantEntry    string
		wantDir      string
	}{
		{"file to file path renames", true, "/app/config.json", "config.json", "/app"},
		{"file to nested file path", true, "/workspace/data/b.txt", "b.txt", "/workspace/data"},
		{"file to dir with trailing slash keeps name", true, "/workspace/data/", "", "/workspace/data/"},
		{"file to dir with /. keeps name", true, "/workspace/data/.", "", "/workspace/data/."},
		{"directory source keeps current behavior", false, "/dest", "", "/dest"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotEntry, gotDir := cpUploadTarget(c.srcIsRegular, c.remotePath)
			if gotEntry != c.wantEntry || gotDir != c.wantDir {
				t.Errorf("cpUploadTarget(%v, %q) = (%q, %q), want (%q, %q)",
					c.srcIsRegular, c.remotePath, gotEntry, gotDir, c.wantEntry, c.wantDir)
			}
		})
	}
}
