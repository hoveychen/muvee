-- domain_only projects have no git source; allow empty string in the check constraint.
ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_git_source_check;
ALTER TABLE projects ADD CONSTRAINT projects_git_source_check
    CHECK (git_source IN ('external', 'hosted', ''));
