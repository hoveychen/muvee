-- 052: Index project_traffic(observed_at) so retention stops sequential-scanning
-- the whole table every hour.
--
-- Why this exists: retention (PurgeOldProjectTraffic) filters purely on
-- observed_at — it deliberately takes no project_id, because rows must expire
-- for projects nobody is looking at too. The only index on the table was
-- (project_id, observed_at DESC), whose leading column is wrong for that
-- predicate, so every purge pass planned a Parallel Seq Scan over the entire
-- table. On a 3GB production node the table had grown to 1.75GB / 4.2M rows
-- (one project alone polls often enough to contribute 3.4M of them), far more
-- than the page cache could hold, and the hourly sweep turned into a read-IO
-- storm: 368k blocks/s, load 51, 45 processes blocked in IO, node rebooted.
--
-- Plain CREATE INDEX, not CONCURRENTLY: the migration runner sends each file as
-- one implicit transaction, and CONCURRENTLY cannot run inside a transaction
-- block. On a fresh deployment the table is empty, so the write lock is
-- momentary. An existing deployment that already built the index by hand (with
-- CONCURRENTLY, under this exact name) is skipped by IF NOT EXISTS.
CREATE INDEX IF NOT EXISTS project_traffic_observed_at
  ON project_traffic (observed_at);
