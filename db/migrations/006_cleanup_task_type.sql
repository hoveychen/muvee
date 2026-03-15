-- Allow 'cleanup' as a valid task type for cross-node container removal.
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_type_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_type_check CHECK (type IN ('build', 'deploy', 'cleanup'));
