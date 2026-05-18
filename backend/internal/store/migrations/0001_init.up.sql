-- Initial schema for ContextForge.

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ============== Users (admin UI) ==============
CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email         TEXT        NOT NULL UNIQUE,
    password_hash TEXT        NOT NULL,
    role          TEXT        NOT NULL CHECK (role IN ('admin','editor','viewer')),
    disabled      BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============== Database/REST connections ==============
CREATE TABLE connections (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name             TEXT        NOT NULL UNIQUE,
    type             TEXT        NOT NULL CHECK (type IN ('pg','mysql','mssql','oracle','mongo','firebird','rest')),
    encrypted_config BYTEA       NOT NULL,
    description      TEXT        NOT NULL DEFAULT '',
    created_by       UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============== Tool groups ==============
CREATE TABLE tool_groups (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                TEXT        NOT NULL UNIQUE,
    description         TEXT        NOT NULL DEFAULT '',
    hidden_by_default   BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============== Tools ==============
CREATE TABLE tools (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id        UUID        NOT NULL REFERENCES tool_groups(id) ON DELETE RESTRICT,
    connection_id   UUID        NOT NULL REFERENCES connections(id) ON DELETE RESTRICT,
    slug            TEXT        NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9_]{1,64}$'),
    title           TEXT        NOT NULL DEFAULT '',
    description     TEXT        NOT NULL DEFAULT '',
    query_text      TEXT        NOT NULL,
    params_schema   JSONB       NOT NULL DEFAULT '{"type":"object","properties":{}}'::jsonb,
    output_schema   JSONB,
    row_limit       INTEGER     NOT NULL DEFAULT 1000 CHECK (row_limit > 0),
    timeout_ms      INTEGER     NOT NULL DEFAULT 15000 CHECK (timeout_ms > 0),
    cache_ttl_sec   INTEGER     NOT NULL DEFAULT 0 CHECK (cache_ttl_sec >= 0),
    cache_per_token BOOLEAN     NOT NULL DEFAULT FALSE,
    status          TEXT        NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','active','disabled')),
    version         INTEGER     NOT NULL DEFAULT 1,
    created_by      UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_tools_group   ON tools(group_id);
CREATE INDEX idx_tools_conn    ON tools(connection_id);
CREATE INDEX idx_tools_status  ON tools(status);

-- ============== Tool versions (rollback) ==============
CREATE TABLE tool_versions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tool_id     UUID        NOT NULL REFERENCES tools(id) ON DELETE CASCADE,
    version     INTEGER     NOT NULL,
    payload     JSONB       NOT NULL,
    created_by  UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tool_id, version)
);

-- ============== Tokens (MCP clients) ==============
CREATE TABLE tokens (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                TEXT        NOT NULL,
    prefix              TEXT        NOT NULL UNIQUE,      -- public lookup prefix (e.g. 8 chars)
    hashed_secret       BYTEA       NOT NULL,             -- sha256(secret)
    owner_user_id       UUID        REFERENCES users(id) ON DELETE SET NULL,
    rate_limit_per_min  INTEGER     NOT NULL DEFAULT 120 CHECK (rate_limit_per_min >= 0),
    ip_allowlist        TEXT[]      NOT NULL DEFAULT '{}',
    revoked_at          TIMESTAMPTZ,
    last_used_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_tokens_prefix ON tokens(prefix);

-- ============== Grants (token authorization) ==============
-- Scope: 'tool' or 'group'. Token gains access to that specific tool, or to
-- all (active) tools inside the group.
CREATE TABLE token_grants (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_id                    UUID        NOT NULL REFERENCES tokens(id) ON DELETE CASCADE,
    scope                       TEXT        NOT NULL CHECK (scope IN ('tool','group')),
    target_id                   UUID        NOT NULL,
    rate_limit_per_min_override INTEGER     CHECK (rate_limit_per_min_override IS NULL OR rate_limit_per_min_override >= 0),
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (token_id, scope, target_id)
);
CREATE INDEX idx_grants_token ON token_grants(token_id);

-- ============== Per-token visibility override for groups ==============
CREATE TABLE group_visibility_overrides (
    token_id   UUID        NOT NULL REFERENCES tokens(id) ON DELETE CASCADE,
    group_id   UUID        NOT NULL REFERENCES tool_groups(id) ON DELETE CASCADE,
    show       BOOLEAN     NOT NULL,
    PRIMARY KEY (token_id, group_id)
);

-- ============== Tool executions (audit + metrics) ==============
CREATE TABLE tool_executions (
    id            BIGSERIAL PRIMARY KEY,
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    token_id      UUID        REFERENCES tokens(id) ON DELETE SET NULL,
    tool_id       UUID        REFERENCES tools(id) ON DELETE SET NULL,
    tool_slug     TEXT        NOT NULL,
    params_redacted JSONB,
    duration_ms   INTEGER,
    rows_returned INTEGER,
    cache_hit     BOOLEAN     NOT NULL DEFAULT FALSE,
    status        TEXT        NOT NULL CHECK (status IN ('ok','error','denied','rate_limited','timeout')),
    error_message TEXT,
    client_ip     TEXT
);
CREATE INDEX idx_exec_occurred ON tool_executions(occurred_at DESC);
CREATE INDEX idx_exec_token    ON tool_executions(token_id, occurred_at DESC);
CREATE INDEX idx_exec_tool     ON tool_executions(tool_id, occurred_at DESC);

-- ============== Audit log (admin UI actions) ==============
CREATE TABLE audit_logs (
    id          BIGSERIAL PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_id    UUID        REFERENCES users(id) ON DELETE SET NULL,
    action      TEXT        NOT NULL,
    target      TEXT,
    details     JSONB
);
CREATE INDEX idx_audit_occurred ON audit_logs(occurred_at DESC);
