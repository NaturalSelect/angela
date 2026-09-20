-- +goose Up
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN uncached_input_tokens INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN uncached_input_tokens;
-- +goose StatementEnd
