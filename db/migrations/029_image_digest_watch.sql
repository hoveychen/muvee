-- 029: Image digest watcher for compose projects. Beyond detecting git
-- branch advances (028), compose projects can also redeploy when any
-- referenced container image's digest changes upstream (watchtower-style).
--
-- last_tracked_image_digests is a JSON object mapping the literal image
-- string from docker-compose.yml (e.g. "redis:7-alpine") to its last
-- observed digest (e.g. "sha256:abc..."). Empty object means "not yet
-- recorded"; the watcher seeds the entry on first sight without triggering
-- a redeploy. Only meaningful for project_type='compose'.

ALTER TABLE projects
    ADD COLUMN IF NOT EXISTS last_tracked_image_digests TEXT NOT NULL DEFAULT '{}';

-- Independent ticker, slower than the git poller because registries are
-- accessed over the public internet and rate-limited.
INSERT INTO system_settings (key, value)
    VALUES ('auto_deploy_image_watch_interval_seconds', '600')
    ON CONFLICT (key) DO NOTHING;
