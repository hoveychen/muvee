package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ─── Node failover replacements ─────────────────────────────────────────────
//
// A failover replacement is an ordinary deployment row. What sets it apart is
// the first line of its logs, written when the row is created (a queued row
// has no other log output yet — agent output is only appended after dispatch,
// and user-triggered rows start with empty logs):
//
//	failover: replacing deployment <old-id> on offline node <host> (<node-id>)
//
// The marker lets the deploy queue dispatch it as "redeploy this image
// elsewhere" instead of a rebuild, lets a user deployment always win over it,
// and lets failover count its own failed attempts for backoff — without a
// schema change.

const failoverLogPrefix = "failover: replacing deployment "

// FailoverReplacementLog is the first log line of a failover replacement.
func FailoverReplacementLog(oldID uuid.UUID, nodeHost string, nodeID uuid.UUID) string {
	return fmt.Sprintf("%s%s on offline node %s (%s)\n", failoverLogPrefix, oldID, nodeHost, nodeID)
}

// FailoverReplacedID reports whether d is a failover replacement and, if so,
// which deployment it replaces.
func FailoverReplacedID(d *Deployment) (uuid.UUID, bool) {
	if d == nil || !strings.HasPrefix(d.Logs, failoverLogPrefix) {
		return uuid.Nil, false
	}
	rest := d.Logs[len(failoverLogPrefix):]
	if len(rest) < 36 {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(rest[:36])
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// FailoverBackoff bounds how often failover retries a replacement that keeps
// failing on the healthy node (e.g. its image is gone from the registry).
// After the n-th failed replacement of one deployment within one outage the
// next attempt waits Base·2^(n-1); after MaxAttempts failures failover gives
// up on that deployment until the node is seen again.
type FailoverBackoff struct {
	Base        time.Duration
	MaxAttempts int
}

func (b FailoverBackoff) delay(failures int) time.Duration {
	return b.Base << (failures - 1)
}

// FailoverEnqueue reports what EnqueueFailoverReplacement decided.
type FailoverEnqueue struct {
	// Replacement is the new queued row; nil when nothing was enqueued.
	Replacement *Deployment
	// Skip says why nothing was enqueued.
	Skip string
	// Failures is the number of failed replacements of this deployment in the
	// current outage; GaveUp is set once it reached MaxAttempts.
	Failures int
	GaveUp   bool
	// Retired is set when old was stopped here because RetiredFor, a newer
	// deployment of the project, already runs; the caller cleans up after it.
	Retired    *Deployment
	RetiredFor *Deployment
}

// lockDeployQueue takes the per-project deploy-queue lock for the rest of tx.
func lockDeployQueue(ctx context.Context, tx pgx.Tx, projectID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('deploy-queue:' || $1::text, 0))`, projectID); err != nil {
		return fmt.Errorf("lock deploy queue: %w", err)
	}
	return nil
}

// EnqueueFailoverReplacement queues a replacement for old, which runs on the
// offline node deadNode, under the same per-project lock AdvanceDeployQueue
// takes. It enqueues nothing when:
//
//   - old is no longer running (the completion callback of an earlier
//     replacement retired it);
//   - any other deployment of the project is queued or in flight: its own
//     completion retires old, so failover must not race it;
//   - a newer deployment of the project already runs: old is then retired
//     here (see Retired) instead;
//   - earlier replacements of old failed in this outage and the backoff has
//     not elapsed, or MaxAttempts is reached.
//
// The completion callback does not take the queue lock; the second and third
// rules are what make every window of it safe (see the PG tests).
func (s *Store) EnqueueFailoverReplacement(ctx context.Context, old *Deployment, deadNode *Node, backoff FailoverBackoff) (*FailoverEnqueue, error) {
	res := &FailoverEnqueue{}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := lockDeployQueue(ctx, tx, old.ProjectID); err != nil {
		return nil, err
	}

	var status DeploymentStatus
	var createdAt time.Time
	err = tx.QueryRow(ctx, `SELECT status, created_at FROM deployments WHERE id = $1`, old.ID).Scan(&status, &createdAt)
	if err == pgx.ErrNoRows {
		res.Skip = "the deployment no longer exists"
		return res, nil
	}
	if err != nil {
		return nil, err
	}
	if status != DeploymentStatusRunning {
		res.Skip = fmt.Sprintf("the deployment is %s, no longer running", status)
		return res, nil
	}

	var inFlight int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM deployments
		WHERE project_id = $1 AND id <> $2 AND status IN ('queued','pending','building','deploying')
	`, old.ProjectID, old.ID).Scan(&inFlight); err != nil {
		return nil, err
	}
	if inFlight > 0 {
		res.Skip = "another deployment of the project is queued or in flight; its completion retires this one"
		return res, nil
	}

	// A newer deployment already runs: its completion callback should have
	// retired old but did not get to it (it is not under this lock, so this is
	// either the moment between its two statements or a callback that died
	// there). Finish the job — exactly one side's UPDATE stops old, and only
	// that side cleans up after it.
	var newer Deployment
	err = tx.QueryRow(ctx, `
		SELECT id, node_id FROM deployments
		WHERE project_id = $1 AND id <> $2 AND status = 'running' AND created_at > $3
		ORDER BY created_at DESC LIMIT 1
	`, old.ProjectID, old.ID, createdAt).Scan(&newer.ID, &newer.NodeID)
	if err != nil && err != pgx.ErrNoRows {
		return nil, err
	}
	if err == nil {
		tag, err := tx.Exec(ctx, `
			UPDATE deployments SET status = 'stopped', logs = logs || $2, updated_at = NOW()
			WHERE id = $1 AND status = 'running'
		`, old.ID, fmt.Sprintf("evicted: node offline; superseded by running deployment %s\n", newer.ID))
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		res.Skip = fmt.Sprintf("newer deployment %s already runs", newer.ID)
		if tag.RowsAffected() == 1 {
			res.Retired, res.RetiredFor = old, &newer
		}
		return res, nil
	}

	// Failed replacements of old in the current outage: created after the
	// node was last seen. A node that came back and died again starts afresh.
	var sinceLast float64
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(EXTRACT(EPOCH FROM NOW() - MAX(updated_at)), 0)::float8
		FROM deployments
		WHERE project_id = $1 AND status = 'failed' AND logs LIKE $2 AND created_at > $3
	`, old.ProjectID, failoverLogPrefix+old.ID.String()+" %", deadNode.LastSeenAt).Scan(&res.Failures, &sinceLast); err != nil {
		return nil, err
	}
	if res.Failures >= backoff.MaxAttempts && backoff.MaxAttempts > 0 {
		res.GaveUp = true
		res.Skip = fmt.Sprintf("%d replacement attempts failed in this outage; giving up", res.Failures)
		return res, nil
	}
	if res.Failures > 0 {
		wait := backoff.delay(res.Failures)
		elapsed := time.Duration(sinceLast * float64(time.Second))
		if elapsed < wait {
			res.Skip = fmt.Sprintf("backing off after %d failed replacement attempt(s): next try in %s", res.Failures, (wait - elapsed).Round(time.Second))
			return res, nil
		}
	}

	d := &Deployment{
		ID:        uuid.New(),
		ProjectID: old.ProjectID,
		ImageTag:  old.ImageTag,
		CommitSHA: old.CommitSHA,
		Status:    DeploymentStatusQueued,
		Logs:      FailoverReplacementLog(old.ID, deadNode.Hostname, deadNode.ID),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO deployments (id, project_id, image_tag, commit_sha, status, node_id, host_port, logs, restart_count, oom_killed, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,NULL,0,$6,0,FALSE,$7,$8)
	`, d.ID, d.ProjectID, d.ImageTag, d.CommitSHA, d.Status, d.Logs, d.CreatedAt, d.UpdatedAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	res.Replacement = d
	return res, nil
}

// ─── Node-scoped container status heartbeat ─────────────────────────────────
//
// UpdateDeploymentHostPortByDomainPrefix / UpdateDeploymentRuntimeStatus key a
// report by domain_prefix alone, so a stale container on one node (the orphan
// a failover left behind, back with its node) could rewrite the running
// deployment on another node. The FromNode variants apply a report only to
// running deployments on the reporting node, without a node, on a node row
// that no longer exists, or on a node row that is not live and has the
// reporter's host_ip — the same docker host under an old name (an agent whose
// NODE_NAME changed registers a new row). That fallback keeps single-node
// stacks exactly on the old behaviour; a live node never touches rows of
// another host.

// UpdateDeploymentHostPortFromNode is UpdateDeploymentHostPortByDomainPrefix
// for a report from nodeID; nodes not seen since liveSince count as not live.
func (s *Store) UpdateDeploymentHostPortFromNode(ctx context.Context, nodeID uuid.UUID, liveSince time.Time, domainPrefix string, hostPort int) error {
	_, err := s.db.Exec(ctx, `
		UPDATE deployments d
		SET host_port = $1,
		    updated_at = NOW()
		FROM projects p
		WHERE d.project_id = p.id
		  AND p.domain_prefix = $2
		  AND d.status = 'running'
		  AND d.host_port IS DISTINCT FROM $1
		  AND (d.node_id = $3 OR d.node_id IS NULL
		    OR NOT EXISTS (SELECT 1 FROM nodes n WHERE n.id = d.node_id)
		    OR EXISTS (SELECT 1 FROM nodes n JOIN nodes r ON r.id = $3
		               WHERE n.id = d.node_id AND n.last_seen_at <= $4 AND n.host_ip = r.host_ip))
	`, hostPort, domainPrefix, nodeID, liveSince)
	return err
}

// UpdateDeploymentRuntimeStatusFromNode is UpdateDeploymentRuntimeStatus for
// a report from nodeID; nodes not seen since liveSince count as not live.
func (s *Store) UpdateDeploymentRuntimeStatusFromNode(ctx context.Context, nodeID uuid.UUID, liveSince time.Time, domainPrefix string, restartCount int, oomKilled bool) error {
	_, err := s.db.Exec(ctx, `
		UPDATE deployments d
		SET restart_count = GREATEST(d.restart_count, $1),
		    oom_killed    = d.oom_killed OR $2,
		    updated_at    = NOW()
		FROM projects p
		WHERE d.project_id = p.id
		  AND p.domain_prefix = $3
		  AND d.status = 'running'
		  AND (d.node_id = $4 OR d.node_id IS NULL
		    OR NOT EXISTS (SELECT 1 FROM nodes n WHERE n.id = d.node_id)
		    OR EXISTS (SELECT 1 FROM nodes n JOIN nodes r ON r.id = $4
		               WHERE n.id = d.node_id AND n.last_seen_at <= $5 AND n.host_ip = r.host_ip))
	`, restartCount, oomKilled, domainPrefix, nodeID, liveSince)
	return err
}
