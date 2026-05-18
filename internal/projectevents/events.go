// Package projectevents is a small in-memory ring buffer of platform-side
// events per project: deploy lifecycle transitions, container start/exit,
// restart-count bumps, OOM kills. It feeds `muveectl projects events`.
//
// Scope is intentionally tiny: in-process map, capped at maxPerProject events
// each, lost on server restart. If/when a durable audit log is needed, this
// package becomes the producer side and the consumer can switch to reading
// from the durable store. Outside callers go through Push / Since.
package projectevents

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

const maxPerProject = 200

// Event is one platform-observed transition on a project. Severity is one of
// "info", "warn", "error" — used by the CLI to colorize / filter.
type Event struct {
	ID        int64     `json:"id"`
	ProjectID uuid.UUID `json:"project_id"`
	Type      string    `json:"type"`
	Severity  string    `json:"severity"`
	Message   string    `json:"message"`
	At        time.Time `json:"at"`
}

// Event severity constants. Keep this list short; the CLI matches on it.
const (
	SeverityInfo  = "info"
	SeverityWarn  = "warn"
	SeverityError = "error"
)

// Common event Type constants. New types are just strings — callers are
// expected to be defensive on unknown types so we don't need to keep a
// closed enum.
const (
	TypeDeployStarted   = "deploy.started"
	TypeDeployCompleted = "deploy.completed"
	TypeDeployFailed    = "deploy.failed"
	TypeContainerStart  = "container.start"
	TypeContainerExit   = "container.exit"
	TypeContainerOOM    = "container.oom_killed"
	TypeRestart         = "restart"
)

type ring struct {
	mu      sync.Mutex
	nextID  int64
	events  []Event
}

func (r *ring) push(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	e.ID = r.nextID
	r.events = append(r.events, e)
	if len(r.events) > maxPerProject {
		// Drop the oldest events when over capacity.
		drop := len(r.events) - maxPerProject
		r.events = append([]Event(nil), r.events[drop:]...)
	}
}

func (r *ring) since(sinceID int64, limit int) []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	if limit <= 0 || limit > maxPerProject {
		limit = maxPerProject
	}
	out := make([]Event, 0, len(r.events))
	for _, e := range r.events {
		if e.ID > sinceID {
			out = append(out, e)
		}
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

var (
	mu      sync.Mutex
	buffers = map[uuid.UUID]*ring{}
)

func ringFor(projectID uuid.UUID) *ring {
	mu.Lock()
	defer mu.Unlock()
	r, ok := buffers[projectID]
	if !ok {
		r = &ring{}
		buffers[projectID] = r
	}
	return r
}

// Push records an event for a project. Safe to call from any goroutine.
func Push(projectID uuid.UUID, evType, severity, message string) {
	ringFor(projectID).push(Event{
		ProjectID: projectID,
		Type:      evType,
		Severity:  severity,
		Message:   message,
		At:        time.Now().UTC(),
	})
}

// Since returns events newer than sinceID, capped at limit. Pass 0 for sinceID
// on the first call; the caller is expected to track the last returned ID and
// pass it next time for follow-style polling.
func Since(projectID uuid.UUID, sinceID int64, limit int) []Event {
	return ringFor(projectID).since(sinceID, limit)
}
