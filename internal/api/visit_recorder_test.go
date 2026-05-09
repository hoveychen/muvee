package api

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hoveychen/muvee/internal/store"
)

func TestAccumulateVisit_NewKeyInserts(t *testing.T) {
	m := make(map[[2]uuid.UUID]*store.ProjectVisit)
	pid, uid := uuid.New(), uuid.New()
	now := time.Now()
	accumulateVisit(m, visitEvent{ProjectID: pid, UserID: uid, SeenAt: now})
	got, ok := m[[2]uuid.UUID{pid, uid}]
	if !ok {
		t.Fatal("expected entry to be created")
	}
	if got.VisitCount != 1 {
		t.Errorf("count=%d want 1", got.VisitCount)
	}
	if !got.LastSeenAt.Equal(now) {
		t.Errorf("last_seen=%v want %v", got.LastSeenAt, now)
	}
}

func TestAccumulateVisit_RepeatBumpsCountAndAdvancesTime(t *testing.T) {
	m := make(map[[2]uuid.UUID]*store.ProjectVisit)
	pid, uid := uuid.New(), uuid.New()
	t1 := time.Now()
	t2 := t1.Add(2 * time.Second)
	accumulateVisit(m, visitEvent{ProjectID: pid, UserID: uid, SeenAt: t1})
	accumulateVisit(m, visitEvent{ProjectID: pid, UserID: uid, SeenAt: t2})
	got := m[[2]uuid.UUID{pid, uid}]
	if got.VisitCount != 2 {
		t.Errorf("count=%d want 2", got.VisitCount)
	}
	if !got.LastSeenAt.Equal(t2) {
		t.Errorf("last_seen=%v want %v", got.LastSeenAt, t2)
	}
}

func TestAccumulateVisit_OutOfOrderDoesNotMoveTimeBackwards(t *testing.T) {
	m := make(map[[2]uuid.UUID]*store.ProjectVisit)
	pid, uid := uuid.New(), uuid.New()
	t2 := time.Now()
	t1 := t2.Add(-5 * time.Second) // older
	accumulateVisit(m, visitEvent{ProjectID: pid, UserID: uid, SeenAt: t2})
	accumulateVisit(m, visitEvent{ProjectID: pid, UserID: uid, SeenAt: t1})
	got := m[[2]uuid.UUID{pid, uid}]
	if !got.LastSeenAt.Equal(t2) {
		t.Errorf("last_seen=%v want unchanged %v (out-of-order arrival should not regress)", got.LastSeenAt, t2)
	}
	if got.VisitCount != 2 {
		t.Errorf("count=%d want 2 (both events still counted)", got.VisitCount)
	}
}

func TestVisitRecorderPush_NonBlockingWhenFull(t *testing.T) {
	// Tiny buffer so we can saturate it deterministically in the test, then
	// confirm a follow-up Push returns without blocking instead of deadlocking
	// on the channel send.
	r := &visitRecorder{ch: make(chan visitEvent, 1)}
	pid, uid := uuid.New(), uuid.New()
	r.Push(pid, uid) // fills the buffer

	done := make(chan struct{})
	go func() {
		r.Push(pid, uid) // would block on a naive send; must drop instead
		close(done)
	}()
	select {
	case <-done:
		// success — Push returned even though buffer was full
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Push blocked when buffer full; expected non-blocking drop")
	}
}

func TestVisitRecorderPush_NilReceiverIsSafe(t *testing.T) {
	// A nil *visitRecorder must not panic — Server is occasionally
	// constructed in tests without StartBackgroundWorkers.
	var r *visitRecorder
	r.Push(uuid.New(), uuid.New())
}
