package api

import (
	"testing"
	"time"
)

// A bad TRAFFIC_RETENTION value must not turn into "keep nothing": a zero or
// negative window would make the next sweep delete every traffic row, and an
// unparseable one used to be impossible to express at all.
func TestTrafficRetentionWindow(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want time.Duration
	}{
		{"unset falls back", "", defaultTrafficRetention},
		{"blank falls back", "   ", defaultTrafficRetention},
		{"override honoured", "48h", 48 * time.Hour},
		{"compound override honoured", "36h30m", 36*time.Hour + 30*time.Minute},
		{"unparseable falls back", "two days", defaultTrafficRetention},
		{"zero falls back", "0s", defaultTrafficRetention},
		{"negative falls back", "-24h", defaultTrafficRetention},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TRAFFIC_RETENTION", tc.env)
			if got := trafficRetentionWindow(); got != tc.want {
				t.Fatalf("trafficRetentionWindow() with TRAFFIC_RETENTION=%q = %s, want %s", tc.env, got, tc.want)
			}
		})
	}
}
