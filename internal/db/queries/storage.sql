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

-- Instance-wide on purpose: only the background jobs call this.
-- name: ListAllConnectionsForJobs :many
SELECT *
FROM storage_connections
ORDER BY created_at, id;

-- A failed check keeps the last known usage, which is better than nothing.
-- name: RecordConnectionCheck :exec
UPDATE storage_connections
SET checked_at = now(),
    check_ok = sqlc.arg(check_ok)::boolean,
    used_bytes = CASE WHEN sqlc.arg(check_ok)::boolean THEN sqlc.arg(used_bytes)::bigint ELSE used_bytes END
WHERE id = sqlc.arg(id);

-- Instance-wide on purpose: only the admin's status page calls this.
-- name: GetConnectionTotals :one
SELECT count(*) AS accounts,
       coalesce(sum(used_bytes), 0)::bigint AS used_bytes
FROM storage_connections;

-- Instance-wide on purpose: only the admin's status page calls this.
-- name: ListUnreachableConnections :many
SELECT c.settings, c.checked_at::timestamptz AS checked_at, u.email AS owner_email
FROM storage_connections c
JOIN users u ON u.id = c.user_id
WHERE c.check_ok = false
ORDER BY c.checked_at, c.id;

-- name: SetConnectionDrainingForOwner :execrows
UPDATE storage_connections
SET draining = $3
WHERE user_id = $1 AND id = $2;

-- name: GetUploadRoutingForOwner :one
SELECT *
FROM upload_routing
WHERE user_id = $1;

-- name: SetUploadRoutingForOwner :exec
INSERT INTO upload_routing (user_id, mode, account_order)
VALUES ($1, $2, $3)
ON CONFLICT (user_id) DO UPDATE
SET mode = EXCLUDED.mode, account_order = EXCLUDED.account_order;

-- name: SetLastUploadConnectionForOwner :exec
UPDATE upload_routing
SET last_connection_id = $2
WHERE user_id = $1;
