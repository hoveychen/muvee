-- +migrate Up

-- Track whether a user is authorized to use the platform (create projects, etc.).
-- Defaults to TRUE so existing users remain authorized.
ALTER TABLE users ADD COLUMN authorized BOOLEAN NOT NULL DEFAULT TRUE;

-- Authorization requests: users request access, admins approve/reject.
CREATE TABLE authorization_requests (
    id          UUID        PRIMARY KEY,
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status      TEXT        NOT NULL DEFAULT 'pending',  -- 'pending' | 'approved' | 'rejected'
    reviewed_by UUID        REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_authorization_requests_user_id ON authorization_requests(user_id);
CREATE INDEX idx_authorization_requests_status  ON authorization_requests(status);

-- +migrate Down

DROP TABLE IF EXISTS authorization_requests;
ALTER TABLE users DROP COLUMN IF EXISTS authorized;
