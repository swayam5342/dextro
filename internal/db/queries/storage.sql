-- name: CreateConnection :one
INSERT INTO storage_connections (id, user_id, provider, name, settings, secret_key_id, secret_ciphertext)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListConnectionsForOwner :many
SELECT *
FROM storage_connections
WHERE user_id = $1
ORDER BY created_at, id;

-- name: GetConnectionForOwner :one
SELECT *
FROM storage_connections
WHERE user_id = $1 AND id = $2;

-- name: GetConnectionByNameForOwner :one
SELECT *
FROM storage_connections
WHERE user_id = $1 AND name = $2;

-- name: UpdateConnectionForOwner :one
UPDATE storage_connections
SET settings = $3,
    secret_key_id = $4,
    secret_ciphertext = $5,
    updated_at = now()
WHERE user_id = $1 AND id = $2
RETURNING *;

-- name: DeleteConnectionForOwner :execrows
DELETE FROM storage_connections
WHERE user_id = $1 AND id = $2;
