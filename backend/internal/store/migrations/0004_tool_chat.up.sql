-- Persistent assistant conversation and last preview result per tool.
-- chat_log: array of {role, content, ts} objects (most recent N kept by API).
-- last_test: snapshot of the most recent dry-run for context-injection on
-- subsequent assistant turns.
ALTER TABLE tools
    ADD COLUMN chat_log JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN last_test JSONB;
