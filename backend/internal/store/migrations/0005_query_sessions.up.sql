-- Query Studio session history (Fase 2b).
-- A lightweight, per-user record of a query-building session: the connection it
-- targets, a title, the current SQL, the assistant chat transcript and the last
-- execution plan. Unlike tools, sessions are NOT versioned and are outside the
-- scope of the encrypted backup (like tokens/users).
CREATE TABLE query_sessions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id UUID        NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
    title         TEXT        NOT NULL,
    query_text    TEXT        NOT NULL DEFAULT '',
    chat_log      JSONB       NOT NULL DEFAULT '[]'::jsonb,
    last_explain  JSONB,                 -- last execution plan (nullable)
    created_by    UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_query_sessions_created_by ON query_sessions(created_by, updated_at DESC);
CREATE INDEX idx_query_sessions_conn       ON query_sessions(connection_id);
