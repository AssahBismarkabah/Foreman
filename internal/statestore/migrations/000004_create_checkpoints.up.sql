CREATE TABLE IF NOT EXISTS checkpoints (
    id          BIGSERIAL PRIMARY KEY,
    session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    snapshot    JSONB NOT NULL DEFAULT '{}',
    step_number INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_checkpoints_session ON checkpoints (session_id, step_number DESC);
