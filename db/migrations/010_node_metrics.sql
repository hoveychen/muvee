CREATE TABLE node_metrics (
    id               UUID             PRIMARY KEY,
    node_id          UUID             NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    collected_at     TIMESTAMPTZ      NOT NULL DEFAULT NOW(),
    cpu_percent      DOUBLE PRECISION NOT NULL DEFAULT 0,
    mem_total_bytes  BIGINT           NOT NULL DEFAULT 0,
    mem_used_bytes   BIGINT           NOT NULL DEFAULT 0,
    disk_total_bytes BIGINT           NOT NULL DEFAULT 0,
    disk_used_bytes  BIGINT           NOT NULL DEFAULT 0,
    load1            DOUBLE PRECISION NOT NULL DEFAULT 0,
    load5            DOUBLE PRECISION NOT NULL DEFAULT 0,
    load15           DOUBLE PRECISION NOT NULL DEFAULT 0
);

CREATE INDEX node_metrics_node_time ON node_metrics (node_id, collected_at DESC);
