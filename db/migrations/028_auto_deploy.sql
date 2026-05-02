-- 028: Auto-deploy support — projects can opt into automatic redeploy when
-- their tracked git branch advances. For external git sources (GitHub/GitLab),
-- a control-plane poller compares `git ls-remote` HEAD against
-- last_tracked_commit_sha. For hosted (muvee-served) repos, the git smart-HTTP
-- handler triggers immediately after a successful receive-pack.
--
-- last_tracked_commit_sha lives on the project (not on a deployment row) so we
-- can detect changes even when previous deployments were stopped/failed.

ALTER TABLE projects ADD COLUMN IF NOT EXISTS auto_deploy_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE projects ADD COLUMN IF NOT EXISTS last_tracked_commit_sha TEXT NOT NULL DEFAULT '';

-- Global kill switch + poll interval (seconds) for the external-repo poller.
INSERT INTO system_settings (key, value)
    VALUES ('auto_deploy_master_enabled', 'true')
    ON CONFLICT (key) DO NOTHING;
INSERT INTO system_settings (key, value)
    VALUES ('auto_deploy_poll_interval_seconds', '60')
    ON CONFLICT (key) DO NOTHING;
