-- Build project type: a project whose deploy step only builds + pushes a
-- Docker image to muvee's own registry, without launching a container, allocating
-- a host port, or registering a Traefik route. Downstream `compose` or `image`
-- projects reference the resulting `last_image_tag` (e.g. via env interpolation)
-- to consume the freshly built image.
--
-- `triggers_redeploy_of` is a JSON array of project UUIDs to auto-redeploy after
-- a successful build push (the "auto-chain" behaviour from PRD section 9).

ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_project_type_check;
ALTER TABLE projects ADD CONSTRAINT projects_project_type_check
    CHECK (project_type IN ('deployment', 'domain_only', 'compose', 'image', 'build'));

ALTER TABLE projects ADD COLUMN IF NOT EXISTS last_image_tag TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS triggers_redeploy_of TEXT NOT NULL DEFAULT '[]';
