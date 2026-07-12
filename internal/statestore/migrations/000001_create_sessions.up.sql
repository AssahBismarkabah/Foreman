CREATE TABLE IF NOT EXISTS sessions (
    id            TEXT PRIMARY KEY,
    task_id       TEXT NOT NULL,
    user_id       TEXT,
    plugin_id     TEXT,
    status        TEXT NOT NULL,
    checkpoint_ref TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions (status);
CREATE INDEX IF NOT EXISTS idx_sessions_created_at ON sessions (created_at DESC);
