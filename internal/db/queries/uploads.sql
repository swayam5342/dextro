-- name: CreateUploadSession :exec
INSERT INTO upload_sessions (id, user_id, connection_id, folder_id, name, content_type, size, last_modified, encrypted, key_id, key_ciphertext)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, sqlc.narg('key_id')::text, $10);

-- name: SetUploadSecretForOwner :execrows
UPDATE upload_sessions
SET secret_key_id = $3,
    secret_ciphertext = $4,
    updated_at = now()
WHERE user_id = $1 AND id = $2;

-- name: GetUploadSessionForOwner :one
SELECT * FROM upload_sessions WHERE user_id = $1 AND id = $2;

-- name: LockUploadSessionForOwner :one
SELECT * FROM upload_sessions WHERE user_id = $1 AND id = $2 FOR UPDATE;

-- name: ListUploadSessionsForOwner :many
SELECT *
FROM upload_sessions
WHERE user_id = $1 AND file_id IS NULL AND secret_ciphertext IS NOT NULL
ORDER BY created_at, id;

-- name: SetUploadReceivedForOwner :exec
UPDATE upload_sessions
SET received = $3,
    hash_state = $4,
    updated_at = now()
WHERE user_id = $1 AND id = $2;

-- The address is no use once the upload is done, so it isn't kept.
-- name: FinishUploadSessionForOwner :exec
UPDATE upload_sessions
SET file_id = $3,
    received = size,
    secret_key_id = NULL,
    secret_ciphertext = NULL,
    key_id = NULL,
    key_ciphertext = NULL,
    hash_state = NULL,
    updated_at = now()
WHERE user_id = $1 AND id = $2;

-- name: DeleteUnfinishedUploadForOwner :execrows
DELETE FROM upload_sessions
WHERE user_id = $1 AND id = $2 AND file_id IS NULL;

-- A finished upload's space shows up in the account's own usage. Files waiting to move
-- are held the same way, on the account they're moving to.
-- name: ListReservedBytesForOwner :many
SELECT held.connection_id, sum(held.size)::bigint AS reserved
FROM (
    SELECT u.connection_id, u.size
    FROM upload_sessions u
    WHERE u.user_id = $1 AND u.file_id IS NULL
    UNION ALL
    SELECT t.target_connection_id, t.size
    FROM file_transfers t
    WHERE t.user_id = $1 AND t.status = 'waiting'
) held
GROUP BY held.connection_id;

-- Lets one user's uploads take turns choosing an account. Accounts belong to one
-- user, so nobody else's uploads are involved.
-- name: LockUploadReservationsForOwner :exec
SELECT pg_advisory_xact_lock(hashtextextended('dextro.upload_reservations:' || sqlc.arg(user_id)::text, 0));

-- Instance-wide on purpose: only the background cleanup calls this.
-- name: ListStaleUploadSessions :many
SELECT *
FROM upload_sessions
WHERE updated_at < $1
ORDER BY updated_at, id;

-- Goes by the time as well, in case the upload came back to life just now.
-- name: DeleteStaleUploadSession :execrows
DELETE FROM upload_sessions
WHERE id = $1 AND updated_at < $2;
