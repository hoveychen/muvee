package api

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/hoveychen/muvee/internal/store"
)

// visitRecorder buffers ForwardAuth allow events in memory and flushes them
// to project_visits in batches. The hot ForwardAuth path calls Push (a
// non-blocking channel send); a background goroutine started via Run does
// the actual UPSERT batching.
//
// Channel-full events are dropped with a warn log — under sustained overload
// we'd rather lose visit counters than slow down request authorization.
type visitRecorder struct {
	store    *store.Store
	ch       chan visitEvent
	interval time.Duration
	batchSz  int
}

type visitEvent struct {
	ProjectID uuid.UUID
	UserID    uuid.UUID
	SeenAt    time.Time
}

const (
	visitChannelBuffer = 1024
	visitFlushInterval = 5 * time.Second
	visitFlushBatch    = 200
)

func newVisitRecorder(st *store.Store) *visitRecorder {
	return &visitRecorder{
		store:    st,
		ch:       make(chan visitEvent, visitChannelBuffer),
		interval: visitFlushInterval,
		batchSz:  visitFlushBatch,
	}
}

// Push enqueues a visit. Non-blocking: drops with a warn if the buffer is full.
func (r *visitRecorder) Push(projectID, userID uuid.UUID) {
	if r == nil {
		return
	}
	select {
	case r.ch <- visitEvent{ProjectID: projectID, UserID: userID, SeenAt: time.Now()}:
	default:
		log.Printf("visit_recorder: buffer full, dropping visit (project=%s user=%s)", projectID, userID)
	}
}

// Run consumes events until ctx is cancelled, flushing on every tick or once
// the in-memory aggregate reaches batchSz. On shutdown it drains anything
// still in the channel and does one final flush.
func (r *visitRecorder) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	pending := make(map[[2]uuid.UUID]*store.ProjectVisit)
	flush := func() {
		if len(pending) == 0 {
			return
		}
		items := make([]store.ProjectVisit, 0, len(pending))
		for _, v := range pending {
			items = append(items, *v)
		}
		// Use a fresh background context so a cancelled parent (shutdown) still
		// gets one final UPSERT through to the DB.
		flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := r.store.RecordProjectVisitsBatch(flushCtx, items); err != nil {
			log.Printf("visit_recorder: batch flush of %d rows failed: %v", len(items), err)
		}
		cancel()
		pending = make(map[[2]uuid.UUID]*store.ProjectVisit)
	}
	for {
		select {
		case <-ctx.Done():
		drainLoop:
			for {
				select {
				case ev := <-r.ch:
					accumulateVisit(pending, ev)
				default:
					break drainLoop
				}
			}
			flush()
			return
		case ev := <-r.ch:
			accumulateVisit(pending, ev)
			if len(pending) >= r.batchSz {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func accumulateVisit(m map[[2]uuid.UUID]*store.ProjectVisit, ev visitEvent) {
	key := [2]uuid.UUID{ev.ProjectID, ev.UserID}
	if existing, ok := m[key]; ok {
		existing.VisitCount++
		if ev.SeenAt.After(existing.LastSeenAt) {
			existing.LastSeenAt = ev.SeenAt
		}
		return
	}
	m[key] = &store.ProjectVisit{
		ProjectID:  ev.ProjectID,
		UserID:     ev.UserID,
		LastSeenAt: ev.SeenAt,
		VisitCount: 1,
	}
}
