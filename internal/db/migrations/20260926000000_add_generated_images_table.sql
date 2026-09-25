-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS generated_images (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    tool_call_id TEXT NOT NULL DEFAULT '',
    prompt TEXT NOT NULL,
    revised_prompt TEXT NOT NULL DEFAULT '',
    source_image_ids TEXT NOT NULL DEFAULT '[]',
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    width INTEGER NOT NULL DEFAULT 0,
    height INTEGER NOT NULL DEFAULT 0,
    data BLOB NOT NULL,
    created_at INTEGER NOT NULL,
    FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_generated_images_session_id ON generated_images (session_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_generated_images_session_id;
DROP TABLE IF EXISTS generated_images;
-- +goose StatementEnd
