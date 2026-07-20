package domains

import "testing"

func TestRebaseHost(t *testing.T) {
	bases := []string{"muveeai.com", "muvee.ai"}
	tests := []struct {
		name       string
		rawURL     string
		targetBase string
		want       string
	}{
		{"subdomain to other base", "https://app.muveeai.com/auth/feishu/callback", "muvee.ai", "https://app.muvee.ai/auth/feishu/callback"},
		{"apex to other base", "https://muveeai.com/_oauth/google", "muvee.ai", "https://muvee.ai/_oauth/google"},
		{"already target base is no-op", "https://app.muveeai.com/x", "muveeai.com", "https://app.muveeai.com/x"},
		{"host matches no base is no-op", "https://other.example.org/x", "muvee.ai", "https://other.example.org/x"},
		{"empty target is no-op", "https://app.muveeai.com/x", "", "https://app.muveeai.com/x"},
		{"port preserved", "https://app.muveeai.com:8443/_oauth/feishu", "muvee.ai", "https://app.muvee.ai:8443/_oauth/feishu"},
		{"base URL with no path", "https://app.muveeai.com", "muvee.ai", "https://app.muvee.ai"},
		{"deep subdomain keeps all labels", "https://a.b.muveeai.com/x", "muvee.ai", "https://a.b.muvee.ai/x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RebaseHost(tt.rawURL, bases, tt.targetBase); got != tt.want {
				t.Errorf("RebaseHost(%q, _, %q) = %q, want %q", tt.rawURL, tt.targetBase, got, tt.want)
			}
		})
	}
}
