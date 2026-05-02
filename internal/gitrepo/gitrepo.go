// Package gitrepo manages bare git repositories on disk and serves the
// Git Smart HTTP protocol for push/pull operations.
package gitrepo

import (
	"compress/flate"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// RepoPath returns the on-disk path for a project's bare repository.
func RepoPath(basePath string, projectID uuid.UUID) string {
	return filepath.Join(basePath, projectID.String()+".git")
}

// InitBareRepo creates a new bare git repository at repoPath.
func InitBareRepo(repoPath string) error {
	if err := os.MkdirAll(filepath.Dir(repoPath), 0755); err != nil {
		return fmt.Errorf("mkdir parent: %w", err)
	}
	cmd := exec.Command("git", "init", "--bare", repoPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git init --bare: %s: %w", string(out), err)
	}
	// Enable the http.receivepack config so pushes via HTTP work.
	cfg := exec.Command("git", "-C", repoPath, "config", "http.receivepack", "true")
	if out, err := cfg.CombinedOutput(); err != nil {
		return fmt.Errorf("git config: %s: %w", string(out), err)
	}
	return nil
}

// DeleteRepo removes a bare repository from disk.
func DeleteRepo(repoPath string) error {
	return os.RemoveAll(repoPath)
}

// AuthFunc authenticates an HTTP request and returns the user ID.
// It receives the project ID extracted from the URL so that access
// control can be checked.
type AuthFunc func(r *http.Request, projectID uuid.UUID) error

// PostReceiveFunc is invoked after a successful git-receive-pack so the caller
// can react to a push (e.g. trigger an auto-deploy). It runs synchronously
// before the HTTP response completes; the caller should return quickly.
type PostReceiveFunc func(ctx context.Context, projectID uuid.UUID, repoPath string)

// reInfoRefs matches /git/{uuid}.git/info/refs
// reService matches /git/{uuid}.git/git-{service}
var (
	reInfoRefs = regexp.MustCompile(`^/git/([0-9a-f-]{36})\.git/info/refs$`)
	reService  = regexp.MustCompile(`^/git/([0-9a-f-]{36})\.git/(git-(?:upload-pack|receive-pack))$`)
)

// HTTPHandler returns an http.Handler that implements the Git Smart HTTP
// protocol. It should be mounted at the server root (it matches /git/...).
// onPostReceive may be nil; when provided, it is called after each successful
// git-receive-pack so callers can wire push-triggered behaviour.
func HTTPHandler(basePath string, authFn AuthFunc, onPostReceive PostReceiveFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// --- info/refs (discovery) ---
		if m := reInfoRefs.FindStringSubmatch(path); m != nil {
			projectID, err := uuid.Parse(m[1])
			if err != nil {
				http.Error(w, "invalid project id", http.StatusBadRequest)
				return
			}
			service := r.URL.Query().Get("service")
			if service != "git-upload-pack" && service != "git-receive-pack" {
				http.Error(w, "invalid service", http.StatusBadRequest)
				return
			}
			if err := authFn(r, projectID); err != nil {
				w.Header().Set("WWW-Authenticate", `Basic realm="Muvee Git"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			repoPath := RepoPath(basePath, projectID)
			if _, err := os.Stat(repoPath); os.IsNotExist(err) {
				http.Error(w, "repository not found", http.StatusNotFound)
				return
			}
			serveInfoRefs(w, r, repoPath, service)
			return
		}

		// --- git-upload-pack / git-receive-pack (data) ---
		if m := reService.FindStringSubmatch(path); m != nil {
			projectID, err := uuid.Parse(m[1])
			if err != nil {
				http.Error(w, "invalid project id", http.StatusBadRequest)
				return
			}
			service := m[2]
			if err := authFn(r, projectID); err != nil {
				w.Header().Set("WWW-Authenticate", `Basic realm="Muvee Git"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			repoPath := RepoPath(basePath, projectID)
			if _, err := os.Stat(repoPath); os.IsNotExist(err) {
				http.Error(w, "repository not found", http.StatusNotFound)
				return
			}
			ok := serveService(w, r, repoPath, service)
			if ok && service == "git-receive-pack" && onPostReceive != nil {
				onPostReceive(r.Context(), projectID, repoPath)
			}
			return
		}

		http.NotFound(w, r)
	})
}

// serveInfoRefs handles GET /info/refs?service=git-{upload,receive}-pack
func serveInfoRefs(w http.ResponseWriter, r *http.Request, repoPath, service string) {
	w.Header().Set("Content-Type", fmt.Sprintf("application/x-%s-advertisement", service))
	w.Header().Set("Cache-Control", "no-cache")

	cmd := exec.CommandContext(r.Context(), "git", service[4:], "--stateless-rpc", "--advertise-refs", repoPath)
	out, err := cmd.Output()
	if err != nil {
		http.Error(w, fmt.Sprintf("git %s: %v", service, err), http.StatusInternalServerError)
		return
	}

	// Write the smart-http pkt-line header followed by the command output.
	header := fmt.Sprintf("# service=%s\n", service)
	pktLine := fmt.Sprintf("%04x%s", len(header)+4, header)
	w.Write([]byte(pktLine))
	w.Write([]byte("0000"))
	w.Write(out)
}

// serveService handles POST /git-{upload,receive}-pack. Returns true when the
// underlying git command exited cleanly so callers can wire post-success
// behaviour (e.g. an auto-deploy trigger after a successful receive-pack).
func serveService(w http.ResponseWriter, r *http.Request, repoPath, service string) bool {
	w.Header().Set("Content-Type", fmt.Sprintf("application/x-%s-result", service))
	w.Header().Set("Cache-Control", "no-cache")

	// Decompress request body if gzip or deflate encoded.
	body := r.Body
	switch r.Header.Get("Content-Encoding") {
	case "gzip":
		gz, err := gzip.NewReader(body)
		if err != nil {
			http.Error(w, "bad gzip", http.StatusBadRequest)
			return false
		}
		defer gz.Close()
		body = gz
	case "deflate":
		body = flate.NewReader(body)
	}

	cmd := exec.CommandContext(r.Context(), "git", service[4:], "--stateless-rpc", repoPath)
	cmd.Stdin = body
	cmd.Stdout = w
	cmd.Stderr = io.Discard

	if err := cmd.Run(); err != nil {
		// If we've already started writing, we can't send an HTTP error.
		// The git client will see the broken stream and report the error.
		return false
	}
	return true
}

// HeadSHA returns the current commit SHA at the tip of `branch` in the bare
// repository at repoPath. Returns an empty string if the branch is not
// present (e.g. the repo has no commits, or the push targeted a different
// branch).
func HeadSHA(ctx context.Context, repoPath, branch string) string {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-parse", "refs/heads/"+branch)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ReadBlobAtBranch returns the contents of `path` at the tip of `branch` in
// the bare repository at repoPath. Used by the image-digest watcher to fetch
// docker-compose.yml without checking out a working tree. Returns
// (nil, nil) if the path or the branch does not exist.
func ReadBlobAtBranch(ctx context.Context, repoPath, branch, path string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "show", "refs/heads/"+branch+":"+path)
	out, err := cmd.Output()
	if err != nil {
		// `git show` returns non-zero for missing refs/paths; treat as "not
		// found" so callers can skip without error.
		return nil, nil
	}
	return out, nil
}

// pktFlush is the git pkt-line flush packet.
const pktFlush = "0000"

// IsEmpty returns true if the repository has no commits.
func IsEmpty(repoPath string) bool {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD")
	return cmd.Run() != nil
}

// DefaultBranch returns the default branch name of a bare repo (e.g., "main").
func DefaultBranch(repoPath string) string {
	cmd := exec.Command("git", "-C", repoPath, "symbolic-ref", "--short", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "main"
	}
	return strings.TrimSpace(string(out))
}
