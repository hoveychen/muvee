package api

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hoveychen/muvee/internal/store"
)

// waitForTaskTerminal is the load-bearing piece of the project-delete cleanup
// fix: deleteProject must block until the compose-cleanup task reaches a
// terminal state before it deletes the project row, otherwise the ON DELETE
// CASCADE drops the task and the old container is never torn down.

func TestWaitForTaskTerminal_ReturnsTrueOnCompletion(t *testing.T) {
	id := uuid.New()
	calls := 0
	fetch := func(_ context.Context, got uuid.UUID) (*store.Task, error) {
		if got != id {
			t.Fatalf("fetched wrong task id: got %s want %s", got, id)
		}
		calls++
		status := store.TaskStatusPending
		if calls >= 3 { // pending, pending, then completed
			status = store.TaskStatusCompleted
		}
		return &store.Task{ID: id, Status: status}, nil
	}

	ok := waitForTaskTerminal(context.Background(), id, time.Second, time.Millisecond, fetch)
	if !ok {
		t.Fatalf("expected true once the task reached completed, got false after %d calls", calls)
	}
	if calls < 3 {
		t.Fatalf("expected to poll until completion (>=3 calls), got %d", calls)
	}
}

func TestWaitForTaskTerminal_ReturnsTrueOnFailure(t *testing.T) {
	id := uuid.New()
	fetch := func(_ context.Context, _ uuid.UUID) (*store.Task, error) {
		return &store.Task{ID: id, Status: store.TaskStatusFailed}, nil
	}
	if !waitForTaskTerminal(context.Background(), id, time.Second, time.Millisecond, fetch) {
		t.Fatal("expected true for a failed (terminal) task")
	}
}

func TestWaitForTaskTerminal_TimesOutWhilePending(t *testing.T) {
	id := uuid.New()
	fetch := func(_ context.Context, _ uuid.UUID) (*store.Task, error) {
		return &store.Task{ID: id, Status: store.TaskStatusPending}, nil // never terminal
	}
	start := time.Now()
	if waitForTaskTerminal(context.Background(), id, 30*time.Millisecond, 5*time.Millisecond, fetch) {
		t.Fatal("expected false when the task never reaches a terminal state")
	}
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
		t.Fatalf("returned before the timeout elapsed (%s)", elapsed)
	}
}

func TestWaitForTaskTerminal_ReturnsFalseOnCancel(t *testing.T) {
	id := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	fetch := func(_ context.Context, _ uuid.UUID) (*store.Task, error) {
		return &store.Task{ID: id, Status: store.TaskStatusPending}, nil
	}
	if waitForTaskTerminal(ctx, id, time.Second, 10*time.Millisecond, fetch) {
		t.Fatal("expected false when the context is cancelled")
	}
}
