-- +goose Up
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN gen_output_tokens INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN gen_duration_ms INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN gen_duration_ms;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN gen_output_tokens;
-- +goose StatementEnd
