-- Adds support for code-based tools (JavaScript executed by the embedded
-- goja runtime). Query-backed tools continue to be the default.
--
-- - tools.kind: 'query' (default, existing behaviour) or 'code'.
-- - tools.connection_id: now nullable, since code tools may not target a
--   single connection. A partial CHECK enforces that query tools still
--   reference a connection.

ALTER TABLE tools
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'query'
    CHECK (kind IN ('query','code'));

ALTER TABLE tools
    ALTER COLUMN connection_id DROP NOT NULL;

ALTER TABLE tools
    ADD CONSTRAINT tools_query_requires_connection
    CHECK (kind <> 'query' OR connection_id IS NOT NULL);

CREATE INDEX idx_tools_kind ON tools(kind);
