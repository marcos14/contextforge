-- Persists the "context" pinned by the user when editing a code tool:
-- which connections (data sources) and which other tools the LLM should
-- always have full schema/details about, even on subsequent edits.
--
-- Shape: {"connection_ids": ["<uuid>", ...], "tool_slugs": ["...", ...]}
ALTER TABLE tools
    ADD COLUMN code_refs JSONB NOT NULL
    DEFAULT '{"connection_ids":[],"tool_slugs":[]}'::jsonb;
