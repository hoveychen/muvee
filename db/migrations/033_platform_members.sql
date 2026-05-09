-- Split platform authorization out of the users table.
-- users now stores identity only (email, name, avatar, etc.); platform_members
-- records who has access to the muvee admin plane and at what role.
--
-- Subdomain auth users (ensured via /api/internal/auth/identity-upsert from
-- cmd/muvee/authservice.go) only land in users; they never become
-- platform_members unless they also have a platform-side relationship — that
-- is what the backfill below seeds, and what EnsurePlatformMember enforces
-- going forward.
--
-- The legacy users.role and users.authorized columns are kept by this
-- migration so the running server can still read them during the rollout
-- window. A follow-up migration (034) will drop them once the new code path
-- is verified in production.

CREATE TABLE IF NOT EXISTS platform_members (
    user_id    UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member')),
    authorized BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Backfill: only insert users with evidence of platform-side activity, so the
-- subdomain-auth-only users that 0087792 started writing to users do not get
-- silently promoted to platform members.
--
-- Five sources of evidence (any one is enough):
--   1. role = 'admin'
--   2. listed in project_members (collaborator on a project)
--   3. owner of any project
--   4. listed in dataset_members
--   5. owner of any dataset
--
-- role and authorized are sourced from users so any earlier admin promotion or
-- invite-mode authorization decision is preserved across the split.
INSERT INTO platform_members (user_id, role, authorized, created_at)
SELECT u.id, u.role, u.authorized, NOW()
FROM users u
WHERE u.role = 'admin'
   OR EXISTS (SELECT 1 FROM project_members pm WHERE pm.user_id = u.id)
   OR EXISTS (SELECT 1 FROM projects        p  WHERE p.owner_id = u.id)
   OR EXISTS (SELECT 1 FROM dataset_members dm WHERE dm.user_id = u.id)
   OR EXISTS (SELECT 1 FROM datasets        d  WHERE d.owner_id = u.id)
ON CONFLICT (user_id) DO NOTHING;
