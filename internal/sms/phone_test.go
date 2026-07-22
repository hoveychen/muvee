package sms

import "testing"

func TestNormalizePhone(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"bare CN mobile", "13800138000", "+8613800138000", false},
		{"with spaces", " 138 0013 8000 ", "+8613800138000", false},
		{"with dashes", "138-0013-8000", "+8613800138000", false},
		{"e164 CN", "+8613800138000", "+8613800138000", false},
		{"86 prefix bare", "8613800138000", "+8613800138000", false},
		{"0086 prefix", "008613800138000", "+8613800138000", false},
		{"foreign e164", "+14155552671", "+14155552671", false},
		{"empty", "", "", true},
		{"too short", "12345", "", true},
		{"cn wrong prefix (12)", "12800138000", "", true},
		{"cn too long", "138001380000", "", true},
		{"non-digits", "138abcd8000", "", true},
		{"e164 non-digits", "+86138x0138000", "", true},
		{"+86 wrong shape", "+8612345", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizePhone(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NormalizePhone(%q) = %q, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizePhone(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("NormalizePhone(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
