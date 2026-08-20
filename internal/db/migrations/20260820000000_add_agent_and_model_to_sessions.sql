-- +goose Up
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN agent TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN model TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE messages ADD COLUMN agent TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE messages DROP COLUMN agent;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN model;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN agent;
-- +goose StatementEnd
