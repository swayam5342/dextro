-- name: CreateAPIKey :one
INSERT INTO api_keys (user_id, name, token_hash, prefix, scopes)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListAPIKeysForUser :many
SELECT *
FROM api_keys
WHERE user_id = $1
ORDER BY created_at DESC, id;

-- name: DeleteAPIKeyForUser :execrows
DELETE FROM api_keys WHERE user_id = $1 AND id = $2;

-- The key is the credential, so this goes by hashed token.
-- name: GetAPIKeyByTokenHash :one
SELECT k.id, k.user_id, k.scopes, k.last_used_at, u.must_change_password
FROM api_keys k
JOIN users u ON u.id = k.user_id
WHERE k.token_hash = $1;

-- name: TouchAPIKey :exec
UPDATE api_keys SET last_used_at = now() WHERE id = $1;
