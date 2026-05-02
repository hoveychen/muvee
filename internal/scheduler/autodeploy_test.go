package scheduler

import "testing"

func TestParseLsRemoteHead(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "single line",
			in:   "abc123def456\trefs/heads/main\n",
			want: "abc123def456",
		},
		{
			name: "no trailing newline",
			in:   "abc123def456\trefs/heads/main",
			want: "abc123def456",
		},
		{
			name: "branch not present",
			in:   "",
			want: "",
		},
		{
			name: "garbage line skipped",
			in:   "warning: redirecting\nabc123\trefs/heads/main\n",
			want: "abc123",
		},
		{
			name: "first matching line wins",
			in:   "shaA\trefs/heads/main\nshaB\trefs/heads/main\n",
			want: "shaA",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseLsRemoteHead(tc.in)
			if got != tc.want {
				t.Fatalf("parseLsRemoteHead(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
