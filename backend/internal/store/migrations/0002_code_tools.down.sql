DROP INDEX IF EXISTS idx_tools_kind;

ALTER TABLE tools DROP CONSTRAINT IF EXISTS tools_query_requires_connection;

-- Restore NOT NULL only if no code tools exist (which would have NULL).
UPDATE tools SET connection_id = connection_id WHERE connection_id IS NULL;
ALTER TABLE tools ALTER COLUMN connection_id SET NOT NULL;

ALTER TABLE tools DROP COLUMN IF EXISTS kind;
