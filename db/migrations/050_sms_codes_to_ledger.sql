-- SMS verification is now delegated to Aliyun 号码认证服务 (PNVS): the platform
-- generates and verifies the code, so the server no longer stores a code hash
-- or runs local expiry/attempt logic. The sms_verification_codes table is
-- repurposed as a lightweight SEND LEDGER: one row per code-send, used only for
-- per-phone rate limiting (CountSMSCodesSince). Relax the columns that only the
-- old local-verification flow populated so a ledger row can be inserted with
-- just (id, phone, project_id, created_at). Existing rows are unaffected;
-- columns are kept (not dropped) to preserve history.
ALTER TABLE sms_verification_codes
    ALTER COLUMN code_hash DROP NOT NULL,
    ALTER COLUMN expires_at DROP NOT NULL;
