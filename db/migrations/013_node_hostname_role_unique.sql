-- Change the unique constraint on nodes from hostname alone to (hostname, role).
-- This allows the same physical host to run both a builder and a deploy agent
-- without colliding, and is the key used by UpsertNode to detect re-registration.

ALTER TABLE nodes DROP CONSTRAINT nodes_hostname_key;
ALTER TABLE nodes ADD CONSTRAINT nodes_hostname_role_key UNIQUE (hostname, role);
