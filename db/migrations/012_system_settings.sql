-- System-wide key-value settings (branding, onboarding state, etc.)
CREATE TABLE IF NOT EXISTS system_settings (
    key        VARCHAR(255) PRIMARY KEY,
    value      TEXT         NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Seed default values
INSERT INTO system_settings (key, value) VALUES
    ('onboarded',  'false'),
    ('site_name',  ''),
    ('logo_url',   ''),
    ('favicon_url', '')
ON CONFLICT (key) DO NOTHING;

-- Node health reports: store the latest self-check results from each agent
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS health_report JSONB;
