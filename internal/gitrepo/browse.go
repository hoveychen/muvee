package gitrepo

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// TreeEntry represents a single entry in a git tree listing.
type TreeEntry struct {
	Name string `json:"name"`
	Type string `json:"type"` // "blob" or "tree"
	Size int64  `json:"size"`
	Path string `json:"path"`
}

// Commit represents a single commit in the log.
type Commit struct {
	SHA     string `json:"sha"`
	Message string `json:"message"`
	Author  string `json:"author"`
	Date    string `json:"date"`
}

// Branch represents a git branch.
type Branch struct {
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
}

// ListTree returns directory entries at the given ref and path.
func ListTree(repoPath, ref, path string) ([]TreeEntry, error) {
	if ref == "" {
		ref = "HEAD"
	}
	// Build the tree-ish: ref or ref:path
	treeish := ref
	if path != "" && path != "/" && path != "." {
		path = strings.TrimPrefix(path, "/")
		path = strings.TrimSuffix(path, "/")
		treeish = ref + ":" + path
	}

	// -l for size, --name-only is not used because we need metadata.
	cmd := exec.Command("git", "-C", repoPath, "ls-tree", "-l", treeish)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-tree: %w", err)
	}

	var entries []TreeEntry
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		entry, err := parseLsTreeLine(line, path)
		if err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// parseLsTreeLine parses a single line from `git ls-tree -l`.
// Format: <mode> <type> <hash> <size>\t<name>
func parseLsTreeLine(line, parentPath string) (TreeEntry, error) {
	tabIdx := strings.IndexByte(line, '\t')
	if tabIdx < 0 {
		return TreeEntry{}, fmt.Errorf("no tab in ls-tree line")
	}
	meta := line[:tabIdx]
	name := line[tabIdx+1:]

	parts := strings.Fields(meta)
	if len(parts) < 4 {
		return TreeEntry{}, fmt.Errorf("unexpected meta fields")
	}

	entryType := parts[1] // "blob" or "tree"
	sizeStr := parts[3]   // size or "-" for trees

	var size int64
	if sizeStr != "-" {
		size, _ = strconv.ParseInt(strings.TrimSpace(sizeStr), 10, 64)
	}

	fullPath := name
	if parentPath != "" && parentPath != "/" && parentPath != "." {
		fullPath = strings.TrimSuffix(parentPath, "/") + "/" + name
	}

	return TreeEntry{
		Name: name,
		Type: entryType,
		Size: size,
		Path: fullPath,
	}, nil
}

// ReadBlob returns the content of a file at the given ref and path.
func ReadBlob(repoPath, ref, path string) ([]byte, error) {
	if ref == "" {
		ref = "HEAD"
	}
	path = strings.TrimPrefix(path, "/")
	obj := ref + ":" + path
	cmd := exec.Command("git", "-C", repoPath, "show", obj)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git show %s: %w", obj, err)
	}
	return out, nil
}

// ListCommits returns recent commits on the given ref.
func ListCommits(repoPath, ref string, limit int) ([]Commit, error) {
	if ref == "" {
		ref = "HEAD"
	}
	if limit <= 0 {
		limit = 20
	}

	// Use null-delimited custom format for easy parsing.
	cmd := exec.Command("git", "-C", repoPath, "log",
		"--format=format:%H%x00%s%x00%an%x00%aI",
		fmt.Sprintf("-%d", limit),
		ref,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}

	var commits []Commit
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x00", 4)
		if len(parts) < 4 {
			continue
		}
		commits = append(commits, Commit{
			SHA:     parts[0],
			Message: parts[1],
			Author:  parts[2],
			Date:    parts[3],
		})
	}
	return commits, nil
}

// ListBranches returns all branches in the repository.
func ListBranches(repoPath string) ([]Branch, error) {
	defaultBranch := DefaultBranch(repoPath)

	cmd := exec.Command("git", "-C", repoPath, "branch", "--format=%(refname:short)")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git branch: %w", err)
	}

	var branches []Branch
	for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if name == "" {
			continue
		}
		branches = append(branches, Branch{
			Name:      name,
			IsDefault: name == defaultBranch,
		})
	}
	return branches, nil
}
