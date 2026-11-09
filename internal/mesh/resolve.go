package mesh

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Resolution is the control plane's answer to GET /api/agent/mesh/resolve.
type Resolution struct {
	Allowed           bool   `json:"allowed"`
	Reason            string `json:"reason"`
	CallerProjectID   string `json:"caller_project_id"`
	CallerProjectName string `json:"caller_project_name"`
	TargetProjectID   string `json:"target_project_id"`
	Backend           string `json:"backend"`
}

// Denial reasons returned by the control plane when Allowed is false.
const (
	ReasonCallerNotFound   = "caller_not_found"
	ReasonTargetNotFound   = "target_not_found"
	ReasonDifferentOwner   = "different_owner"
	ReasonAccessDisabled   = "access_disabled"
	ReasonTargetNotRunning = "target_not_running"
)

// statusForReason maps a denial reason to the HTTP status the caller sees.
func statusForReason(reason string) int {
	switch reason {
	case ReasonCallerNotFound, ReasonTargetNotFound:
		return http.StatusNotFound
	case ReasonDifferentOwner, ReasonAccessDisabled:
		return http.StatusForbidden
	case ReasonTargetNotRunning:
		return http.StatusServiceUnavailable
	default:
		return http.StatusForbidden
	}
}

type resolver interface {
	Resolve(ctx context.Context, caller, target string) (*Resolution, error)
}

// httpResolver calls the control plane's resolve endpoint.
type httpResolver struct {
	baseURL string
	secret  string
	client  *http.Client
}

func newHTTPResolver(baseURL, secret string) *httpResolver {
	return &httpResolver{
		baseURL: strings.TrimRight(baseURL, "/"),
		secret:  secret,
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

func (r *httpResolver) Resolve(ctx context.Context, caller, target string) (*Resolution, error) {
	q := url.Values{"caller": {caller}, "target": {target}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/api/agent/mesh/resolve?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if r.secret != "" {
		req.Header.Set("X-Agent-Secret", r.secret)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mesh resolve returned HTTP %d", resp.StatusCode)
	}
	var res Resolution
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("decode mesh resolve: %w", err)
	}
	return &res, nil
}

// cachedResolver memoises answers (allowed or denied) for ttl. Transport
// errors are not cached.
type cachedResolver struct {
	next resolver
	ttl  time.Duration
	now  func() time.Time

	mu      sync.Mutex
	entries map[[2]string]cachedResolution
}

type cachedResolution struct {
	res     *Resolution
	expires time.Time
}

func newCachedResolver(next resolver, ttl time.Duration) *cachedResolver {
	return &cachedResolver{next: next, ttl: ttl, now: time.Now, entries: map[[2]string]cachedResolution{}}
}

func (c *cachedResolver) Resolve(ctx context.Context, caller, target string) (*Resolution, error) {
	key := [2]string{caller, target}
	now := c.now()
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && now.Before(e.expires) {
		c.mu.Unlock()
		return e.res, nil
	}
	c.mu.Unlock()

	res, err := c.next.Resolve(ctx, caller, target)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	for k, e := range c.entries { // keep the map from growing without bound
		if !now.Before(e.expires) {
			delete(c.entries, k)
		}
	}
	c.entries[key] = cachedResolution{res: res, expires: now.Add(c.ttl)}
	c.mu.Unlock()
	return res, nil
}
