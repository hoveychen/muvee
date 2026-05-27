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
	// Stub: returns a non-blocking limiter. Replaced in P4.
	return &BuildLimiter{}
}

// Acquire takes one slot. It blocks until a slot is free or ctx is done.
// On ctx cancellation it returns ctx.Err() and does NOT consume a slot.
func (l *BuildLimiter) Acquire(ctx context.Context) error {
	// Stub: never blocks. Replaced in P4.
	return nil
}

// Release frees the slot taken by a prior successful Acquire.
func (l *BuildLimiter) Release() {
	// Stub: no-op. Replaced in P4.
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
		return 0 // stub: should be defaultBuildMaxConcurrent
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0 // stub: should be defaultBuildMaxConcurrent
	}
	return n
}
