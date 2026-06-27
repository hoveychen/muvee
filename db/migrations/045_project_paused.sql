-- 045: Soft-pause support for projects.
--
-- A paused project has its container(s) stopped via `docker stop` (CPU/memory
-- released, image+volumes kept) but its config, deployment row, and domain
-- routing are all preserved. Resume issues `docker start` — no rebuild, no
-- re-pull, no compose re-up. `paused` is the single source of truth for the
-- runtime state; the active deployment row keeps its `running` status so we can
-- still locate the pinned node to dispatch the unpause task to.
--
-- While paused, every deploy path is gated in scheduler.TriggerDeployment so a
-- manual deploy, the auto-deploy poller, the image-digest watcher, and the
-- git-push hook all refuse to redeploy until the project is resumed.

ALTER TABLE projects ADD COLUMN IF NOT EXISTS paused BOOLEAN NOT NULL DEFAULT FALSE;

-- Agent task types for the stop/start operations behind pause/resume.
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_type_check;
ALTER TABLE tasks
    ADD CONSTRAINT tasks_type_check
    CHECK (type IN ('build', 'deploy', 'cleanup', 'runtime_logs', 'restart', 'env', 'describe', 'pause', 'unpause'));
