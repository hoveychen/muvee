-- 016_tunnel_history.sql
-- Records adhoc tunnel connection history for the admin dashboard.
CREATE TABLE tunnel_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain TEXT NOT NULL,
    user_email TEXT NOT NULL,
    auth_required BOOLEAN NOT NULL DEFAULT TRUE,
    connected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    disconnected_at TIMESTAMPTZ
);
CREATE INDEX idx_tunnel_history_connected ON tunnel_history(connected_at DESC);
