-- +goose Up
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN base_message_id TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN base_message_id;
-- +goose StatementEnd
