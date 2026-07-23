package deployer

import "testing"

func TestParseUserGID(t *testing.T) {
	cases := []struct {
		name        string
		user        string
		wantUID     int
		wantGID     int
		wantResolve bool
	}{
		{"empty is root resolved", "", 0, 0, true},
		{"whitespace is root resolved", "  ", 0, 0, true},
		{"numeric uid only defaults gid to uid", "1000", 1000, 1000, true},
		{"numeric uid and gid", "1000:2000", 1000, 2000, true},
		{"non-root numeric", "10001", 10001, 10001, true},
		{"named user unresolved", "relay", 0, 0, false},
		{"uid with named group unresolved", "1000:staff", 0, 0, false},
		{"named user with named group unresolved", "relay:relay", 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uid, gid, resolved := parseUserGID(tc.user)
			if resolved != tc.wantResolve {
				t.Fatalf("parseUserGID(%q) resolved = %v, want %v", tc.user, resolved, tc.wantResolve)
			}
			if !tc.wantResolve {
				return // uid/gid meaningless when unresolved
			}
			if uid != tc.wantUID || gid != tc.wantGID {
				t.Errorf("parseUserGID(%q) = %d:%d, want %d:%d", tc.user, uid, gid, tc.wantUID, tc.wantGID)
			}
		})
	}
}
