package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
)

// handleBrandingUpload accepts a multipart file upload for branding assets
// (logo or favicon). It saves the file with a content-hash name and returns
// the public serving URL.
//
// POST /api/admin/branding/upload
// Form field: "file" (the image file)
// Query param: "type" = "logo" | "favicon"
func (s *Server) handleBrandingUpload(w http.ResponseWriter, r *http.Request) {
	if s.brandingDir == "" {
		jsonErr(w, fmt.Errorf("branding uploads are not configured (BRANDING_DIR not set)"), http.StatusInternalServerError)
		return
	}

	assetType := r.URL.Query().Get("type")
	if assetType != "logo" && assetType != "favicon" {
		jsonErr(w, fmt.Errorf("query param 'type' must be 'logo' or 'favicon'"), http.StatusBadRequest)
		return
	}

	if err := r.ParseMultipartForm(10 << 20); err != nil { // 10 MB limit
		jsonErr(w, fmt.Errorf("file too large or invalid multipart form"), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		jsonErr(w, fmt.Errorf("missing form field 'file'"), http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Determine extension from original filename
	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowedExts := map[string]bool{
		".png": true, ".jpg": true, ".jpeg": true,
		".gif": true, ".svg": true, ".ico": true, ".webp": true,
	}
	if !allowedExts[ext] {
		jsonErr(w, fmt.Errorf("unsupported file type %q; allowed: png, jpg, jpeg, gif, svg, ico, webp", ext), http.StatusBadRequest)
		return
	}

	// Read file content
	data, err := io.ReadAll(file)
	if err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}

	// Hash-based filename to avoid collisions and enable caching
	hash := sha256.Sum256(data)
	shortHash := hex.EncodeToString(hash[:8])
	filename := fmt.Sprintf("%s-%s%s", assetType, shortHash, ext)

	// Ensure directory exists
	if err := os.MkdirAll(s.brandingDir, 0o755); err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}

	dst := filepath.Join(s.brandingDir, filename)
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}

	// Return the public URL
	publicURL := fmt.Sprintf("/api/public/branding/%s", filename)

	// Auto-update the corresponding setting
	settingKey := assetType + "_url"
	if err := s.store.SetSetting(r.Context(), settingKey, publicURL); err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]string{"url": publicURL})
}

// handleServeBranding serves uploaded branding assets from the branding directory.
//
// GET /api/public/branding/{filename}
func (s *Server) handleServeBranding(w http.ResponseWriter, r *http.Request) {
	if s.brandingDir == "" {
		http.NotFound(w, r)
		return
	}

	filename := chi.URLParam(r, "filename")

	// Sanitize: only allow simple filenames, no path traversal
	if filename == "" || strings.ContainsAny(filename, "/\\") || strings.Contains(filename, "..") {
		http.NotFound(w, r)
		return
	}

	fpath := filepath.Join(s.brandingDir, filename)
	if _, err := os.Stat(fpath); os.IsNotExist(err) {
		http.NotFound(w, r)
		return
	}

	// Allow long-term caching since filenames contain content hashes
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, fpath)
}
