CREATE TABLE container_metrics (
    id               UUID         PRIMARY KEY,
    deployment_id    UUID         NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    collected_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    cpu_percent      DOUBLE PRECISION NOT NULL DEFAULT 0,
    mem_usage_bytes  BIGINT       NOT NULL DEFAULT 0,
    mem_limit_bytes  BIGINT       NOT NULL DEFAULT 0,
    net_rx_bytes     BIGINT       NOT NULL DEFAULT 0,
    net_tx_bytes     BIGINT       NOT NULL DEFAULT 0,
    block_read_bytes  BIGINT      NOT NULL DEFAULT 0,
    block_write_bytes BIGINT      NOT NULL DEFAULT 0
);

CREATE INDEX container_metrics_deployment_time ON container_metrics (deployment_id, collected_at DESC);
