-- name: CreateSession :one
INSERT INTO sessions (user_id, token_hash, user_agent, ip, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- The session cookie is the credential, so this goes by hashed token.
-- name: GetSessionByTokenHash :one
SELECT s.id, s.user_id, s.last_seen_at, u.role, u.must_change_password
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1 AND s.expires_at > now();

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = now() WHERE id = $1;

-- name: ListSessionsForUser :many
SELECT *
FROM sessions
WHERE user_id = $1 AND expires_at > now()
ORDER BY created_at DESC, id;

-- name: DeleteSessionForUser :execrows
DELETE FROM sessions WHERE user_id = $1 AND id = $2;

-- name: DeleteOtherSessionsForUser :exec
DELETE FROM sessions WHERE user_id = $1 AND id <> $2;

-- name: DeleteExpiredSessionsForUser :exec
DELETE FROM sessions WHERE user_id = $1 AND expires_at <= now();

-- name: DeleteAllSessionsForUser :exec
DELETE FROM sessions WHERE user_id = $1;
