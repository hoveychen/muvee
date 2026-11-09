package api

import (
	"testing"

	"github.com/google/uuid"

	"github.com/hoveychen/muvee/internal/store"
)

func TestDecideMeshResolve(t *testing.T) {
	owner, other := uuid.New(), uuid.New()
	proj := func(name string, ownerID uuid.UUID) *store.Project {
		return &store.Project{ID: uuid.New(), Name: name, DomainPrefix: name, OwnerID: ownerID, AllowProjectAccess: true}
	}
	running := &store.RunningDeploymentInfo{HostIP: "10.0.0.5", HostPort: 32768}

	caller := proj("caller", owner)
	same := proj("target", owner)
	foreign := proj("foreign", other)
	disabled := proj("disabled", owner)
	disabled.AllowProjectAccess = false
	paused := proj("paused", owner)
	paused.Paused = true

	cases := []struct {
		name        string
		caller      *store.Project
		target      *store.Project
		running     *store.RunningDeploymentInfo
		wantAllowed bool
		wantReason  string
	}{
		{"same owner allowed", caller, same, running, true, ""},
		{"self call allowed", caller, caller, running, true, ""},
		{"caller not found", nil, same, running, false, meshReasonCallerNotFound},
		{"target not found", caller, nil, nil, false, meshReasonTargetNotFound},
		{"different owner", caller, foreign, running, false, meshReasonDifferentOwner},
		{"access disabled", caller, disabled, running, false, meshReasonAccessDisabled},
		{"no running deployment", caller, same, nil, false, meshReasonTargetNotRunning},
		{"paused counts as not running", caller, paused, running, false, meshReasonTargetNotRunning},
		{"running row without host port", caller, same, &store.RunningDeploymentInfo{HostIP: "10.0.0.5"}, false, meshReasonTargetNotRunning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideMeshResolve(tc.caller, tc.target, tc.running)
			if got.Allowed != tc.wantAllowed || got.Reason != tc.wantReason {
				t.Fatalf("allowed=%v reason=%q, want allowed=%v reason=%q", got.Allowed, got.Reason, tc.wantAllowed, tc.wantReason)
			}
			if tc.wantAllowed {
				if got.Backend != "10.0.0.5:32768" {
					t.Fatalf("backend=%q, want 10.0.0.5:32768", got.Backend)
				}
				if got.CallerProjectID != tc.caller.ID.String() || got.CallerProjectName != tc.caller.Name || got.TargetProjectID != tc.target.ID.String() {
					t.Fatalf("ids not filled: %+v", got)
				}
			} else if got.Backend != "" {
				t.Fatalf("backend leaked on deny: %q", got.Backend)
			}
		})
	}
}
