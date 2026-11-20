-- Per-subsite account badge (BACKLOG-20261121-001): when on, the embed-bridge
-- SDK shows a small "Account" link to the host's own /_oauth/me on the
-- project's top-level pages that passed ForwardAuth. authservice reads this
-- column through the internal access check (/_oauth/account-ui); the plugin
-- itself has no switch.
--
-- OFF for every existing project (the column default): a badge would suddenly
-- appear on pages nobody asked to change. New projects get it ON — the
-- createProject handler writes true unless the body says otherwise.
--
-- Rollback: ALTER TABLE projects DROP COLUMN IF EXISTS account_badge;
-- DELETE FROM schema_migrations WHERE filename = '061_project_account_badge.sql';

ALTER TABLE projects
    ADD COLUMN IF NOT EXISTS account_badge BOOLEAN NOT NULL DEFAULT FALSE;
