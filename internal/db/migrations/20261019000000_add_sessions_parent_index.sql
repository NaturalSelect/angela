-- +goose Up
-- +goose StatementBegin
-- Index parent_session_id so listing a session's children avoids a table scan
CREATE INDEX IF NOT EXISTS idx_sessions_parent_session_id ON sessions (parent_session_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_sessions_parent_session_id;
-- +goose StatementEnd
