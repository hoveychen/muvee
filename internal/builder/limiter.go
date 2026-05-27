package builder

import (
	"context"
	"strconv"
	"strings"
)

// defaultBuildMaxConcurrent is the fallback cap when BUILDER_MAX_CONCURRENT is
// unset, blank, unparseable, or non-positive. Picked low because builds on a
// shared host compete with the running deployments for RAM.
const defaultBuildMaxConcurrent = 1

// BuildLimiter caps the number of concurrent builds. Acquire blocks until a
// slot is available or the context is cancelled; every successful Acquire must
// be paired with exactly one Release.
type BuildLimiter struct {
	sem chan struct{}
}

// NewBuildLimiter returns a limiter with the given concurrency cap. Values
// below 1 are treated as 1 to preserve liveness.
func NewBuildLimiter(cap int) *BuildLimiter {
	if cap < 1 {
		cap = 1
	}
	return &BuildLimiter{sem: make(chan struct{}, cap)}
}

// Acquire takes one slot. It blocks until a slot is free or ctx is done.
// On ctx cancellation it returns ctx.Err() and does NOT consume a slot.
func (l *BuildLimiter) Acquire(ctx context.Context) error {
	select {
	case l.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release frees the slot taken by a prior successful Acquire.
func (l *BuildLimiter) Release() {
	<-l.sem
}

// parseBuildMaxConcurrent reads BUILDER_MAX_CONCURRENT from the injected
// getenv. Empty / whitespace / non-integer / non-positive values fall back to
// defaultBuildMaxConcurrent so a typo never silently uncaps parallelism.
//
// getenv injection mirrors buildProxyPassthroughFor so this can be unit-tested
// without touching the process environment.
func parseBuildMaxConcurrent(getenv func(string) string) int {
	raw := strings.TrimSpace(getenv("BUILDER_MAX_CONCURRENT"))
	if raw == "" {
		return defaultBuildMaxConcurrent
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return defaultBuildMaxConcurrent
	}
	return n
}
