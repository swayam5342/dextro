-- Fills in what a sync learned. By the object, because a file that moved in the meantime
-- has a different one.
-- name: SetFileChecksumForObject :exec
UPDATE files
SET checksum = sqlc.arg(checksum),
    content_hash = CASE WHEN encrypted THEN content_hash ELSE coalesce(content_hash, sqlc.arg(checksum)) END
WHERE user_id = sqlc.arg(user_id)
  AND id = sqlc.arg(id)
  AND connection_id = sqlc.arg(connection_id)
  AND object_id = sqlc.arg(object_id);

-- Instance-wide on purpose: only the background check calls this. Files added from Drive
-- are left out, because they're someone's own and can change whenever they like.
-- name: ListFilesToCheck :many
SELECT *
FROM files
WHERE checksum IS NOT NULL AND NOT external
ORDER BY checked_at NULLS FIRST, id
LIMIT $1;

-- name: MarkFileCheckedForOwner :exec
UPDATE files SET checked_at = now() WHERE user_id = $1 AND id = $2;

-- name: RecordFileProblem :exec
INSERT INTO file_problems (user_id, file_id, connection_id, name, size, kind, reported_checksum)
VALUES ($1, sqlc.narg('file_id')::uuid, $2, $3, $4, $5, sqlc.narg('reported_checksum')::text)
ON CONFLICT (file_id, kind) WHERE file_id IS NOT NULL
DO UPDATE SET reported_checksum = EXCLUDED.reported_checksum;

-- name: ClearFileProblemForOwner :exec
DELETE FROM file_problems WHERE user_id = $1 AND file_id = $2 AND kind = $3;

-- name: ListFileProblemsForOwner :many
SELECT *
FROM file_problems
WHERE user_id = $1
ORDER BY found_at DESC, id
LIMIT 500;

-- name: GetFileProblemForOwner :one
SELECT * FROM file_problems WHERE user_id = $1 AND id = $2;

-- name: DeleteFileProblemForOwner :execrows
DELETE FROM file_problems WHERE user_id = $1 AND id = $2;

-- name: AcceptFileChecksumForOwner :exec
UPDATE files
SET checksum = sqlc.arg(checksum),
    content_hash = CASE WHEN encrypted THEN content_hash ELSE sqlc.arg(checksum) END
WHERE user_id = sqlc.arg(user_id) AND id = sqlc.arg(id);

-- name: CountFileChecksForOwner :one
SELECT count(*) FILTER (WHERE checksum IS NOT NULL AND NOT external) AS checkable,
       count(*) FILTER (WHERE checked_at IS NOT NULL) AS checked
FROM files
WHERE user_id = $1;

-- Live files that share their content with another, biggest first. An empty file matches
-- every other empty file, which says nothing.
-- name: ListDuplicateFilesForOwner :many
WITH RECURSIVE hidden AS (
    SELECT f.id FROM folders f WHERE f.user_id = $1 AND f.trashed_at IS NOT NULL
    UNION
    SELECT c.id FROM folders c JOIN hidden ON c.parent_id = hidden.id WHERE c.user_id = $1
)
SELECT files.*
FROM files
WHERE files.user_id = $1
  AND files.size > 0
  AND files.trashed_at IS NULL
  AND (files.folder_id IS NULL OR files.folder_id NOT IN (SELECT id FROM hidden))
  AND (files.content_hash, files.size) IN (
      SELECT d.content_hash, d.size
      FROM files d
      WHERE d.user_id = $1
        AND d.content_hash IS NOT NULL
        AND d.trashed_at IS NULL
        AND (d.folder_id IS NULL OR d.folder_id NOT IN (SELECT id FROM hidden))
      GROUP BY d.content_hash, d.size
      HAVING count(*) > 1
  )
ORDER BY files.size DESC, files.content_hash, files.created_at, files.id
LIMIT 2000;

-- name: ListAllFoldersForOwner :many
SELECT * FROM folders WHERE user_id = $1;
