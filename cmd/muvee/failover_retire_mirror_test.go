package main

import (
	"github.com/google/uuid"

	"github.com/hoveychen/muvee/internal/scheduler"
	"github.com/hoveychen/muvee/internal/store"
)

// retireReplaced is the completion callback's "retire the project's other
// running deployments" step — the same function internal/api completeTask
// calls. (Before the fix this helper mirrored the API's inline block.)
func retireReplaced(f *failoverFixture, sched *scheduler.Scheduler, dep *store.Deployment, taskNodeID *uuid.UUID) {
	sched.RetireReplacedDeployments(f.ctx, dep, taskNodeID)
}
