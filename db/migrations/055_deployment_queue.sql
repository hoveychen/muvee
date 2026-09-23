-- Per-project deploy serialisation: a deployment triggered while another one
-- of the same project is in flight is parked as 'queued'; when the in-flight
-- one finishes only the newest queued row is dispatched and the older ones
-- become 'superseded' (terminal, never dispatched). See
-- store.AdvanceDeployQueue.

ALTER TABLE deployments DROP CONSTRAINT IF EXISTS deployments_status_check;
ALTER TABLE deployments
    ADD CONSTRAINT deployments_status_check
    CHECK (status IN ('pending','building','deploying','running','failed','stopped','queued','superseded'));

CREATE INDEX IF NOT EXISTS idx_deployments_project_status ON deployments (project_id, status);
