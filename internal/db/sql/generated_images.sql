-- name: CreateGeneratedImage :one
INSERT INTO generated_images (
    id,
    session_id,
    tool_call_id,
    prompt,
    revised_prompt,
    source_image_ids,
    provider,
    model,
    mime_type,
    width,
    height,
    data,
    created_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, strftime('%s', 'now')
)
RETURNING *;

-- name: GetGeneratedImage :one
SELECT *
FROM generated_images
WHERE id = ? LIMIT 1;
