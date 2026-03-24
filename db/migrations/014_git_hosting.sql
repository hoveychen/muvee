-- 014_git_hosting: Add git_source column to projects for hosted repository support.
ALTER TABLE projects ADD COLUMN git_source TEXT NOT NULL DEFAULT 'external'
  CHECK (git_source IN ('external', 'hosted'));
