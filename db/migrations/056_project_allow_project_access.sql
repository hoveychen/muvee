-- Same-owner project mesh: a process inside one project's container can call
-- another project's HTTP service at http://<domain_prefix>.muvee.internal with
-- no credentials. The agent's mesh gateway asks the server
-- (GET /api/agent/mesh/resolve) whether caller and target share an owner and
-- injects X-Muvee-Caller-Project / X-Muvee-Caller-Project-Id so the target
-- knows which project is calling.
--
-- allow_project_access is the target-side switch "may other projects of the
-- same owner reach me". ON by default, existing projects included — the mesh
-- only ever connects projects of the same owner, and the owner can turn it off
-- per project in the settings page.

ALTER TABLE projects
    ADD COLUMN IF NOT EXISTS allow_project_access BOOLEAN NOT NULL DEFAULT TRUE;
