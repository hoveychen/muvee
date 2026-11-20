-- The console's "subsite sign-ins" card lists the subsites a user visited
-- most recently (GET /api/me/visited-projects, BACKLOG-20261121-001).
-- project_visits' primary key starts with project_id, so looking a user's rows
-- up would scan the whole table; this index serves WHERE user_id = $1
-- ORDER BY last_seen_at DESC.
--
-- project_visits holds one row per (project, visitor), so the build is short;
-- writes to the table (the visit recorder's batch UPSERTs) wait for it.
--
-- Rollback: DROP INDEX IF EXISTS idx_project_visits_user_lastseen;
-- DELETE FROM schema_migrations WHERE filename = '060_project_visits_user_lastseen.sql';

CREATE INDEX IF NOT EXISTS idx_project_visits_user_lastseen
    ON project_visits(user_id, last_seen_at DESC);
