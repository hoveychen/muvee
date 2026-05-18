package projectevents

import (
	"testing"

	"github.com/google/uuid"
)

func TestPushAndSinceSurfaceNewEvents(t *testing.T) {
	pid := uuid.New()
	Push(pid, TypeDeployStarted, SeverityInfo, "build started")
	Push(pid, TypeDeployCompleted, SeverityInfo, "deployed")
	got := Since(pid, 0, 50)
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}
	if got[0].Type != TypeDeployStarted || got[1].Type != TypeDeployCompleted {
		t.Fatalf("event order wrong: %#v", got)
	}
	// Subsequent Since(..., lastID) only surfaces newer events.
	lastID := got[1].ID
	got2 := Since(pid, lastID, 50)
	if len(got2) != 0 {
		t.Fatalf("expected 0 new events, got %d", len(got2))
	}
	Push(pid, TypeContainerExit, SeverityError, "exit code 1")
	got3 := Since(pid, lastID, 50)
	if len(got3) != 1 || got3[0].Type != TypeContainerExit {
		t.Fatalf("expected the new exit event, got %#v", got3)
	}
}

func TestRingDropsOldestPastCapacity(t *testing.T) {
	pid := uuid.New()
	for i := 0; i < maxPerProject+50; i++ {
		Push(pid, TypeRestart, SeverityInfo, "tick")
	}
	got := Since(pid, 0, maxPerProject+50)
	if len(got) != maxPerProject {
		t.Fatalf("ring should cap at %d, got %d", maxPerProject, len(got))
	}
	// IDs must be monotonically increasing — and the oldest should have been
	// dropped (id 1..50 gone, surface starts at 51).
	if got[0].ID != 51 {
		t.Fatalf("expected first remaining ID to be 51 (oldest 50 dropped), got %d", got[0].ID)
	}
}

func TestSinceLimitRespected(t *testing.T) {
	pid := uuid.New()
	for i := 0; i < 30; i++ {
		Push(pid, TypeRestart, SeverityInfo, "tick")
	}
	got := Since(pid, 0, 10)
	if len(got) != 10 {
		t.Fatalf("expected limit=10, got %d", len(got))
	}
	// Limit returns the *latest* slice — older ones dropped client-side.
	if got[len(got)-1].ID != 30 {
		t.Fatalf("expected last ID 30, got %d", got[len(got)-1].ID)
	}
}
