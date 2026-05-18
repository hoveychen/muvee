-- Expand the tasks.type CHECK constraint to cover the user-dispatched task
-- types added since migration 006: runtime_logs (long-broken on prod —
-- nobody noticed because no one had used it via API), and the kubectl-style
-- inspect tasks added in v1.21.0 (restart / env / describe).

ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_type_check;
ALTER TABLE tasks
    ADD CONSTRAINT tasks_type_check
    CHECK (type IN ('build', 'deploy', 'cleanup', 'runtime_logs', 'restart', 'env', 'describe'));
