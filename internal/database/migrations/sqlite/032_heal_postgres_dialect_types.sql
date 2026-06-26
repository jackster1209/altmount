-- +goose Up
-- +goose StatementBegin
-- No-op: migration 032 heals PostgreSQL-only type bugs (see the postgres/
-- counterpart). SQLite stores these columns correctly and needs no change;
-- this file exists only to keep the migration numbering parallel across dialects.
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- +goose StatementEnd
