// Package traefiklog tails Traefik's JSON access log file and persists each
// request as a ProjectTraffic row, attributing it to a project via the
// subdomain (domain_prefix) extracted from the Host header.
package traefiklog

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hoveychen/muvee/internal/store"
)

// Tailer watches a Traefik JSON access log file and inserts each line as a
// ProjectTraffic row.
type Tailer struct {
	store      *store.Store
	path       string
	baseDomain string
	cache      map[string]cacheEntry
}

type cacheEntry struct {
	id      uuid.UUID
	expires time.Time
}

const cacheTTL = 30 * time.Second

// New returns a Tailer. baseDomain is stripped from incoming Host headers to
// recover the project's domain_prefix (e.g. "foo.apps.example.com" with
// baseDomain "apps.example.com" -> "foo").
func New(st *store.Store, path, baseDomain string) *Tailer {
	return &Tailer{
		store:      st,
		path:       path,
		baseDomain: strings.TrimPrefix(baseDomain, "."),
		cache:      make(map[string]cacheEntry),
	}
}

// Start runs the tail loop until ctx is cancelled. It handles file rotation
// by polling inode/size and reopening the file when it shrinks or is replaced.
func (t *Tailer) Start(ctx context.Context) {
	if t.path == "" {
		log.Println("traefiklog: TRAEFIK_ACCESS_LOG_PATH not set; tailer disabled")
		return
	}
	log.Printf("traefiklog: tailing %s (base domain %q)", t.path, t.baseDomain)
	var (
		file   *os.File
		reader *bufio.Reader
		inode  uint64
		offset int64
	)
	defer func() {
		if file != nil {
			_ = file.Close()
		}
	}()

	openFile := func() {
		if file != nil {
			_ = file.Close()
			file = nil
		}
		f, err := os.Open(t.path)
		if err != nil {
			return
		}
		// Start at end on first open so we don't backfill a huge history.
		if _, err := f.Seek(0, io.SeekEnd); err != nil {
			_ = f.Close()
			return
		}
		fi, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return
		}
		file = f
		reader = bufio.NewReader(f)
		inode = statInode(fi)
		offset = fi.Size()
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if file == nil {
			openFile()
			if file == nil {
				continue
			}
		}
		fi, err := os.Stat(t.path)
		if err != nil {
			_ = file.Close()
			file = nil
			continue
		}
		curInode := statInode(fi)
		curSize := fi.Size()
		if curInode != inode || curSize < offset {
			// Rotated or truncated: reopen from the start of the new file.
			_ = file.Close()
			f, err := os.Open(t.path)
			if err != nil {
				file = nil
				continue
			}
			file = f
			reader = bufio.NewReader(f)
			inode = curInode
			offset = 0
		}
		for {
			line, err := reader.ReadString('\n')
			if len(line) > 0 {
				offset += int64(len(line))
				t.handleLine(ctx, strings.TrimRight(line, "\r\n"))
			}
			if err != nil {
				break
			}
		}
	}
}

// traefikEntry holds the fields we care about from a Traefik JSON access log
// line. Traefik v3 emits every field at the top level; request headers are
// prefixed with `request_`.
type traefikEntry struct {
	ClientHost        string  `json:"ClientHost"`
	StartUTC          string  `json:"StartUTC"`
	RequestMethod     string  `json:"RequestMethod"`
	RequestPath       string  `json:"RequestPath"`
	RequestHost       string  `json:"RequestHost"`
	DownstreamStatus  int     `json:"DownstreamStatus"`
	DurationNS        int64   `json:"Duration"`
	DownstreamSize    int64   `json:"DownstreamContentSize"`
	RequestUserAgent  string  `json:"request_User-Agent"`
	RequestReferer    string  `json:"request_Referer"`
	RouterName        string  `json:"RouterName"`
	ServiceName       string  `json:"ServiceName"`
	OriginDurationSec float64 `json:"OriginDuration"`
}

func (t *Tailer) handleLine(ctx context.Context, line string) {
	if line == "" {
		return
	}
	var e traefikEntry
	if err := json.Unmarshal([]byte(line), &e); err != nil {
		return
	}
	// Strip port from RequestHost if present.
	host := e.RequestHost
	if i := strings.LastIndex(host, ":"); i > 0 && !strings.Contains(host[i+1:], ".") {
		host = host[:i]
	}
	prefix := t.hostToPrefix(host)
	if prefix == "" {
		return
	}
	pid, ok := t.lookupProject(ctx, prefix)
	if !ok {
		return
	}
	observed := time.Now()
	if e.StartUTC != "" {
		if ts, err := time.Parse(time.RFC3339Nano, e.StartUTC); err == nil {
			observed = ts
		}
	}
	entry := &store.ProjectTraffic{
		ProjectID:  pid,
		ObservedAt: observed,
		ClientIP:   stripPort(e.ClientHost),
		Host:       host,
		Method:     e.RequestMethod,
		Path:       truncate(e.RequestPath, 1024),
		Status:     e.DownstreamStatus,
		DurationMs: e.DurationNS / int64(time.Millisecond),
		BytesSent:  e.DownstreamSize,
		UserAgent:  truncate(e.RequestUserAgent, 512),
		Referer:    truncate(e.RequestReferer, 512),
	}
	if err := t.store.InsertProjectTraffic(ctx, entry); err != nil {
		log.Printf("traefiklog: insert failed: %v", err)
	}
}

// hostToPrefix extracts the subdomain (domain_prefix) from a host header by
// stripping the configured base domain. Returns "" if host doesn't match.
func (t *Tailer) hostToPrefix(host string) string {
	host = strings.ToLower(host)
	if t.baseDomain == "" {
		// Fallback: first label.
		if i := strings.IndexByte(host, '.'); i > 0 {
			return host[:i]
		}
		return ""
	}
	base := strings.ToLower(t.baseDomain)
	if host == base {
		return "" // root domain — muvee control plane, not a project
	}
	suffix := "." + base
	if !strings.HasSuffix(host, suffix) {
		return ""
	}
	prefix := strings.TrimSuffix(host, suffix)
	if strings.Contains(prefix, ".") {
		return "" // multi-level subdomain — not a project domain_prefix
	}
	return prefix
}

func (t *Tailer) lookupProject(ctx context.Context, prefix string) (uuid.UUID, bool) {
	now := time.Now()
	if e, ok := t.cache[prefix]; ok && now.Before(e.expires) {
		if e.id == uuid.Nil {
			return uuid.Nil, false
		}
		return e.id, true
	}
	id, err := t.store.ResolveProjectIDByDomainPrefix(ctx, prefix)
	if err != nil {
		return uuid.Nil, false
	}
	t.cache[prefix] = cacheEntry{id: id, expires: now.Add(cacheTTL)}
	if id == uuid.Nil {
		return uuid.Nil, false
	}
	return id, true
}

func stripPort(s string) string {
	if i := strings.LastIndex(s, ":"); i > 0 && !strings.Contains(s[i+1:], ".") {
		return s[:i]
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
