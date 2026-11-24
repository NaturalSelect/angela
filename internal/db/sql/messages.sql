-- name: GetMessage :one
SELECT *
FROM messages
WHERE id = ? LIMIT 1;

-- name: ListMessagesBySession :many
SELECT *
FROM messages
WHERE session_id = ?
ORDER BY rowid ASC;

-- name: ListMessagesBySessionSince :many
SELECT *
FROM messages
WHERE messages.session_id = sqlc.arg(session_id)
  AND messages.rowid >= COALESCE(
    (SELECT m.rowid FROM messages m
     WHERE m.id = sqlc.arg(since_id) AND m.session_id = sqlc.arg(session_id)),
    0)
ORDER BY rowid ASC;

-- name: ListMessagesBySessionTail :many
SELECT *
FROM messages
WHERE session_id = sqlc.arg(session_id)
ORDER BY rowid DESC
LIMIT sqlc.arg(lim);

-- name: ListMessagesBySessionBefore :many
SELECT *
FROM messages
WHERE messages.session_id = sqlc.arg(session_id)
  AND messages.rowid < (SELECT m.rowid FROM messages m
               WHERE m.id = sqlc.arg(before_id) AND m.session_id = sqlc.arg(session_id))
ORDER BY rowid DESC
LIMIT sqlc.arg(lim);

-- name: CreateMessage :one
INSERT INTO messages (
    id,
    session_id,
    role,
    parts,
    model,
    provider,
    agent,
    is_summary_message,
    created_at,
    updated_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, strftime('%s', 'now'), strftime('%s', 'now')
)
RETURNING *;

-- name: UpdateMessage :exec
UPDATE messages
SET
    parts = ?,
    finished_at = ?,
    updated_at = strftime('%s', 'now')
WHERE id = ?;


-- name: DeleteMessage :exec
DELETE FROM messages
WHERE id = ?;

-- name: DeleteSessionMessages :exec
DELETE FROM messages
WHERE session_id = ?;

-- name: ListUserMessagesBySession :many
SELECT *
FROM messages
WHERE session_id = ? AND role = 'user'
ORDER BY rowid DESC;

-- name: ListAllUserMessages :many
SELECT *
FROM messages
WHERE role = 'user'
ORDER BY rowid DESC;

-- name: GetLastAssistantMessageBySession :one
SELECT *
FROM messages
WHERE session_id = ? AND role = 'assistant' AND is_summary_message = 0
ORDER BY rowid DESC
LIMIT 1;
