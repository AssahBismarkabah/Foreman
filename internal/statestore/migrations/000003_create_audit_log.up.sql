CREATE TABLE IF NOT EXISTS audit_log (
    id          BIGSERIAL PRIMARY KEY,
    session_id  TEXT NOT NULL REFERENCES sessions(id),
    user_id     TEXT NOT NULL DEFAULT '',
    action      TEXT NOT NULL,
    target      TEXT NOT NULL DEFAULT '',
    metadata    JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_audit_log_session ON audit_log (session_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_created ON audit_log (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_action  ON audit_log (action);
