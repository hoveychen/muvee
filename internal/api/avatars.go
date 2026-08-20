package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Avatar materialisation exists because Microsoft Entra is the one provider
// that cannot hand over an avatar URL. Its v2.0 ID tokens carry no `picture`
// claim and Graph serves the profile photo as bytes behind a bearer token, so
// internal/auth/entra.go inlines those bytes as a base64 data URI. That value
// then travelled everywhere an avatar URL is expected — including the
// forward-auth session JWT, where it blew past the 4096-byte cookie limit and
// silently killed the session (see cookieSafeAvatar in cmd/muvee/authservice.go).
//
// Writing the bytes to disk once and handing back an ordinary https URL makes
// Entra look exactly like Google (`picture`) and Lark (`avatar_url`) to every
// consumer downstream, which is what the rest of the code already assumes.
//
// Storage reuses the branding asset volume (BRANDING_DIR, a persisted docker
// volume in docker-compose.server.yml) under an avatars/ subdirectory, so no
// new mount or compose change is needed to deploy this.

// avatarMimeExt is the set of image types accepted from a data URI, mapped to
// the extension the file is stored under. Microsoft Graph serves JPEG for
// profile photos; the others are here because nothing stops another provider
// from inlining an avatar later.
var avatarMimeExt = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// maxAvatarBytes caps the decoded image written to disk. internal/auth/entra.go
// already limits its Graph read to 256 KB; this is the independent check on the
// storage side, since materialisation accepts a data URI from any caller.
const maxAvatarBytes = 512 << 10

// avatarDir returns the directory avatars are stored in, or "" when branding
// storage is unconfigured (BRANDING_DIR empty) and materialisation must be
// skipped.
func (s *Server) avatarDir() string {
	if s.brandingDir == "" {
		return ""
	}
	return filepath.Join(s.brandingDir, "avatars")
}

// MaterializeAvatar converts an inlined image data URI into a stored file plus
// a public https URL, and passes anything else (an ordinary provider URL, or
// an empty string) through untouched.
//
// It never fails a login: on any error it logs and returns the input unchanged,
// so the caller still gets a usable — if oversized — avatar value rather than a
// broken sign-in. The cookie-size guard in authservice remains the backstop for
// that case.
func (s *Server) MaterializeAvatar(avatar string) string {
	url, err := s.materializeAvatar(avatar)
	if err != nil {
		log.Printf("api: materialize avatar: %v; keeping the original value", err)
		return avatar
	}
	return url
}

func (s *Server) materializeAvatar(avatar string) (string, error) {
	// Only data URIs need materialising. Google's `picture` and Lark's
	// `avatar_url` are already URLs and must survive byte-for-byte.
	if !strings.HasPrefix(avatar, "data:") {
		return avatar, nil
	}
	dir := s.avatarDir()
	if dir == "" {
		return "", fmt.Errorf("avatar storage is not configured (BRANDING_DIR is empty)")
	}
	mime, data, err := decodeImageDataURI(avatar)
	if err != nil {
		return "", err
	}
	ext, ok := avatarMimeExt[mime]
	if !ok {
		return "", fmt.Errorf("unsupported avatar type %q", mime)
	}
	if len(data) > maxAvatarBytes {
		return "", fmt.Errorf("avatar of %d bytes exceeds the %d byte limit", len(data), maxAvatarBytes)
	}
	if len(data) == 0 {
		return "", fmt.Errorf("avatar data URI decoded to zero bytes")
	}

	// Content-hash name, matching the branding uploads: identical photos
	// collapse onto one file, so a user signing in repeatedly does not pile up
	// copies, and the served URL can be cached immutably.
	hash := sha256.Sum256(data)
	filename := "avatar-" + hex.EncodeToString(hash[:8]) + ext

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create avatar dir: %w", err)
	}
	dst := filepath.Join(dir, filename)
	if _, err := os.Stat(dst); err != nil {
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return "", fmt.Errorf("write avatar: %w", err)
		}
	}
	return s.publicAvatarURL(filename), nil
}

// publicAvatarURL builds the absolute URL for a stored avatar. Absolute rather
// than root-relative because the consumers live on project subdomains and in
// forward-auth headers, not just the platform SPA. The canonical base domain is
// used (not a per-request host): materialisation runs on the internal upsert
// path, where the inbound Host is an internal address, and an <img> load needs
// no same-origin relationship anyway. Falls back to a root-relative path when
// no base domain is configured, which is the local-development case.
func (s *Server) publicAvatarURL(filename string) string {
	path := "/api/public/avatars/" + filename
	if base := strings.TrimSpace(s.baseDomain); base != "" {
		return "https://" + base + path
	}
	return path
}

// decodeImageDataURI splits a base64 image data URI into its mime type and
// decoded bytes. Only the base64 form is accepted — that is what every avatar
// producer emits, and the percent-encoded form would need separate handling.
func decodeImageDataURI(uri string) (mime string, data []byte, err error) {
	const prefix = "data:"
	rest := strings.TrimPrefix(uri, prefix)
	comma := strings.IndexByte(rest, ',')
	if comma < 0 {
		return "", nil, fmt.Errorf("malformed data URI: no comma")
	}
	meta, payload := rest[:comma], rest[comma+1:]
	if !strings.HasSuffix(meta, ";base64") {
		return "", nil, fmt.Errorf("unsupported data URI encoding %q: want base64", meta)
	}
	mime = strings.ToLower(strings.TrimSuffix(meta, ";base64"))
	data, err = base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", nil, fmt.Errorf("decode data URI payload: %w", err)
	}
	return mime, data, nil
}

// handleServeAvatar serves a stored avatar. Public and unauthenticated by
// design: these URLs are loaded by <img> tags on project subdomains, which
// carry no credentials. The content-hash filename is the only thing protecting
// them, so it must stay unguessable — hence sha256 rather than the user id.
//
// GET /api/public/avatars/{filename}
func (s *Server) handleServeAvatar(w http.ResponseWriter, r *http.Request) {
	dir := s.avatarDir()
	if dir == "" {
		http.NotFound(w, r)
		return
	}
	filename := chi.URLParam(r, "filename")
	if !safeAssetFilename(filename) {
		http.NotFound(w, r)
		return
	}
	fpath := filepath.Join(dir, filename)
	if _, err := os.Stat(fpath); os.IsNotExist(err) {
		http.NotFound(w, r)
		return
	}
	// Content-hash filenames make the bytes immutable, so cache hard.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, fpath)
}

// safeAssetFilename rejects anything that is not a bare filename, so a request
// cannot escape the asset directory. Shared by the avatar and branding
// handlers.
func safeAssetFilename(filename string) bool {
	return filename != "" && !strings.ContainsAny(filename, `/\`) && !strings.Contains(filename, "..")
}
