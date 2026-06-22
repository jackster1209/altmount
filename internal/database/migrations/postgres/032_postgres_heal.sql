-- +goose Up
-- +goose StatementBegin
-- Heals PostgreSQL databases created before the dialect type fixes (migrations
-- 001/010/016/027). Every step is guarded against the already-correct state, so
-- this is a no-op on fresh installs and only rewrites columns that are wrong.
--
-- Why this exists: the malformed expression index in 010 aborted the whole
-- migration chain, so no PostgreSQL database could complete setup before these
-- fixes. Any database that exists came from a manually-patched build; this
-- migration brings it to the corrected schema without a rebuild from scratch.
DO $$
BEGIN
    -- import_hourly_stats.bytes_downloaded: INTEGER -> BIGINT (hourly byte
    -- totals overflow int32 quickly on a busy install).
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'import_hourly_stats'
          AND column_name = 'bytes_downloaded'
          AND data_type = 'integer'
    ) THEN
        ALTER TABLE import_hourly_stats ALTER COLUMN bytes_downloaded TYPE BIGINT;
    END IF;

    -- media_files.file_size: INTEGER -> BIGINT (any file > 2.1 GB overflows).
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'media_files'
          AND column_name = 'file_size'
          AND data_type = 'integer'
    ) THEN
        ALTER TABLE media_files ALTER COLUMN file_size TYPE BIGINT;
    END IF;

    -- file_health.metadata: JSONB -> TEXT. The application only ever reads this
    -- column as a JSON string (json.Unmarshal) and never uses a jsonb operator
    -- on it; as JSONB the health query's `metadata != ''` comparison fails with
    -- 22P02 (invalid input syntax for type json).
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'file_health'
          AND column_name = 'metadata'
          AND data_type = 'jsonb'
    ) THEN
        ALTER TABLE file_health ALTER COLUMN metadata TYPE TEXT USING metadata::text;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Ensure the nzbdav lookup index exists in its correct (parenthesized) form.
-- Harmless if already present and correct.
DROP INDEX IF EXISTS idx_import_queue_nzbdav_id;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_import_queue_nzbdav_id
    ON import_queue (((metadata::jsonb)->>'nzbdav_id'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Irreversible by design: we will not narrow BIGINT back to INTEGER (data loss)
-- or re-introduce the broken JSONB typing. No-op.
-- +goose StatementEnd
