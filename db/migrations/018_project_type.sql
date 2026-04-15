ALTER TABLE projects
    ADD COLUMN project_type TEXT NOT NULL DEFAULT 'deployment'
    CHECK (project_type IN ('deployment', 'domain_only'));

ALTER TABLE projects ALTER COLUMN git_url DROP NOT NULL;

ALTER TABLE projects ADD CONSTRAINT projects_owner_name_key UNIQUE (owner_id, name);

CREATE INDEX projects_type_idx ON projects (project_type);
