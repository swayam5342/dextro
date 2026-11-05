-- name: CreateFile :one
INSERT INTO files (id, user_id, connection_id, folder_id, object_id, name, size, content_type)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetFileForOwner :one
SELECT * FROM files WHERE user_id = $1 AND id = $2;

-- name: ListFilesInFolderForOwner :many
SELECT *
FROM files
WHERE user_id = $1
  AND folder_id IS NOT DISTINCT FROM sqlc.narg('folder_id')::uuid
  AND trashed_at IS NULL
ORDER BY lower(name), id;

-- name: RenameFileForOwner :one
UPDATE files
SET name = $3, updated_at = now()
WHERE user_id = $1 AND id = $2
RETURNING *;

-- name: MoveFileForOwner :one
UPDATE files
SET folder_id = sqlc.narg('folder_id')::uuid, updated_at = now()
WHERE user_id = $1 AND id = $2
RETURNING *;

-- name: SetFileTrashedForOwner :execrows
UPDATE files
SET trashed_at = sqlc.narg('trashed_at')::timestamptz, updated_at = now()
WHERE user_id = $1 AND id = $2;

-- name: DeleteFileForOwner :execrows
DELETE FROM files WHERE user_id = $1 AND id = $2;

-- name: ListTrashedFilesForOwner :many
SELECT *
FROM files
WHERE user_id = $1 AND trashed_at IS NOT NULL
ORDER BY trashed_at DESC, id;

-- Every file under a folder at any depth, trashed or not.
-- name: ListFilesUnderFolderForOwner :many
WITH RECURSIVE tree AS (
    SELECT f.id FROM folders f WHERE f.user_id = $1 AND f.id = $2
    -- UNION rather than UNION ALL, so a loop in the tree ends the walk.
    UNION
    SELECT c.id FROM folders c JOIN tree ON c.parent_id = tree.id WHERE c.user_id = $1
)
SELECT files.*
FROM files
JOIN tree ON files.folder_id = tree.id
WHERE files.user_id = $1;

-- name: CreateFolder :one
INSERT INTO folders (user_id, parent_id, name)
VALUES ($1, sqlc.narg('parent_id')::uuid, $2)
RETURNING *;

-- name: GetFolderForOwner :one
SELECT * FROM folders WHERE user_id = $1 AND id = $2;

-- name: ListFoldersInFolderForOwner :many
SELECT *
FROM folders
WHERE user_id = $1
  AND parent_id IS NOT DISTINCT FROM sqlc.narg('parent_id')::uuid
  AND trashed_at IS NULL
ORDER BY lower(name), id;

-- A folder and its ancestors, nearest first.
-- name: ListFolderPathForOwner :many
WITH RECURSIVE path AS (
    SELECT f.*, 0 AS depth FROM folders f WHERE f.user_id = $1 AND f.id = $2
    UNION ALL
    SELECT p.*, path.depth + 1
    FROM folders p
    JOIN path ON p.id = path.parent_id
    WHERE p.user_id = $1
      -- The depth changes every row, so only a limit can end a walk round a loop.
      AND path.depth < 1000
)
SELECT id, user_id, parent_id, name, trashed_at, created_at, updated_at
FROM path
ORDER BY depth;

-- name: RenameFolderForOwner :one
UPDATE folders
SET name = $3, updated_at = now()
WHERE user_id = $1 AND id = $2
RETURNING *;

-- name: MoveFolderForOwner :one
UPDATE folders
SET parent_id = sqlc.narg('parent_id')::uuid, updated_at = now()
WHERE user_id = $1 AND id = $2
RETURNING *;

-- name: SetFolderTrashedForOwner :execrows
UPDATE folders
SET trashed_at = sqlc.narg('trashed_at')::timestamptz, updated_at = now()
WHERE user_id = $1 AND id = $2;

-- name: DeleteFolderForOwner :execrows
DELETE FROM folders WHERE user_id = $1 AND id = $2;

-- name: ListTrashedFoldersForOwner :many
SELECT *
FROM folders
WHERE user_id = $1 AND trashed_at IS NOT NULL
ORDER BY trashed_at DESC, id;

-- Serializes folder moves per user, so two moves can never make a loop.
-- name: LockFolderTreeForOwner :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(user_id)::uuid::text, 1));
