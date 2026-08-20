package auth

import "testing"

// TestMaterializeAvatarHook covers the hook wiring itself: with no materialiser
// installed (the authservice process, which has no asset storage) avatars pass
// through untouched, and with one installed every non-empty value is routed
// through it. The identity-write call sites that use it need a database, so they
// are covered by the end-to-end test instead.
func TestMaterializeAvatarHook(t *testing.T) {
	const dataURI = "data:image/jpeg;base64,AAAA"

	t.Run("no materialiser installed passes through", func(t *testing.T) {
		s := &Service{}
		if got := s.materializeAvatar(dataURI); got != dataURI {
			t.Errorf("got %q, want the input unchanged", got)
		}
	})

	t.Run("installed materialiser is applied", func(t *testing.T) {
		s := &Service{}
		s.SetAvatarMaterializer(func(string) string { return "https://muveeai.com/api/public/avatars/avatar-x.jpg" })
		if got := s.materializeAvatar(dataURI); got != "https://muveeai.com/api/public/avatars/avatar-x.jpg" {
			t.Errorf("got %q, want the materialised URL", got)
		}
	})

	t.Run("empty avatar skips the materialiser", func(t *testing.T) {
		s := &Service{}
		called := false
		s.SetAvatarMaterializer(func(string) string { called = true; return "unexpected" })
		if got := s.materializeAvatar(""); got != "" {
			t.Errorf("got %q, want an empty string", got)
		}
		if called {
			t.Error("materialiser was called for an empty avatar")
		}
	})
}
