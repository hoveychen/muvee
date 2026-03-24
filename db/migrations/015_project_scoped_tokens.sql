-- 015_project_scoped_tokens.sql
-- Make API tokens project-scoped: each token is bound to exactly one project.
ALTER TABLE api_tokens ADD COLUMN project_id UUID REFERENCES projects(id) ON DELETE CASCADE;
CREATE INDEX idx_api_tokens_project ON api_tokens(project_id);
