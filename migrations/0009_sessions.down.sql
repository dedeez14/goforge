DROP INDEX IF EXISTS idx_refresh_tokens_session_id;
ALTER TABLE refresh_tokens DROP COLUMN IF EXISTS session_id;

DROP INDEX IF EXISTS idx_sessions_expires_at;
DROP INDEX IF EXISTS idx_sessions_user_active;
DROP TABLE IF EXISTS sessions;
