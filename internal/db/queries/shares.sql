-- Written before Google is asked. A link that's already finished keeps what it has until
-- Google has agreed to the change.
-- name: PrepareShare :exec
INSERT INTO file_shares (file_id, user_id, role, expires_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (file_id) DO UPDATE
SET updated_at = now()
WHERE file_shares.url = '';

-- name: SaveShareForOwner :one
UPDATE file_shares
SET role = $3, url = $4, expires_at = $5, updated_at = now()
WHERE user_id = $1 AND file_id = $2
RETURNING *;

-- name: DelayShare :exec
UPDATE file_shares SET updated_at = now() WHERE file_id = $1;

-- name: GetShareForOwner :one
SELECT * FROM file_shares WHERE user_id = $1 AND file_id = $2;

-- name: DeleteShareForOwner :execrows
DELETE FROM file_shares WHERE user_id = $1 AND file_id = $2;

-- name: ListSharesForOwner :many
SELECT s.*, f.name
FROM file_shares s
JOIN files f ON f.id = s.file_id
WHERE s.user_id = $1 AND s.url <> ''
ORDER BY lower(f.name), f.id;

-- Everything inside a folder, however deep, including rows Google hasn't confirmed.
-- name: ListSharedFilesUnderFolderForOwner :many
WITH RECURSIVE tree AS (
    SELECT f.id FROM folders f WHERE f.user_id = $1 AND f.id = $2
    -- UNION rather than UNION ALL, so a loop in the tree ends the walk.
    UNION
    SELECT c.id FROM folders c JOIN tree ON c.parent_id = tree.id WHERE c.user_id = $1
)
SELECT s.file_id
FROM file_shares s
JOIN files ON files.id = s.file_id
JOIN tree ON files.folder_id = tree.id
WHERE s.user_id = $1;

-- name: CountSharesForConnection :one
SELECT count(*)
FROM file_shares s
JOIN files f ON f.id = s.file_id
WHERE s.user_id = $1 AND f.connection_id = $2;

-- Instance-wide on purpose: only the background job calls this. It finds links past
-- their expiry, and rows left unfinished by a crash.
-- name: ListDueShares :many
SELECT s.file_id, s.user_id, s.url, f.connection_id, f.object_id
FROM file_shares s
JOIN files f ON f.id = s.file_id
WHERE (s.url <> '' AND s.expires_at <= now())
   OR (s.url = '' AND s.updated_at < sqlc.arg(abandoned_before))
ORDER BY s.updated_at, s.file_id
LIMIT 200;
