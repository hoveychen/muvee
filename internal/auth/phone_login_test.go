package auth

import "testing"

func TestSyntheticPhoneEmail(t *testing.T) {
	cases := map[string]string{
		"+8613800138000": "8613800138000@phone.invalid",
		"8613800138000":  "8613800138000@phone.invalid",
		"+14155552671":   "14155552671@phone.invalid",
	}
	for in, want := range cases {
		if got := SyntheticPhoneEmail(in); got != want {
			t.Errorf("SyntheticPhoneEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPhoneIsOrgScoped guards the domain-check skip: a phone login has no email
// domain, so it must be treated as org-scoped (checkDomain skipped) — otherwise
// a deployment with ALLOWED_DOMAINS set would 401 every phone user.
func TestPhoneIsOrgScoped(t *testing.T) {
	if !isOrgScopedProvider(map[string]Provider{}, "phone") {
		t.Fatal("phone must be org-scoped so checkDomain is skipped")
	}
	// sanity: a normal email provider is not org-scoped
	if isOrgScopedProvider(map[string]Provider{}, "google") {
		t.Fatal("google should not be org-scoped")
	}
}
