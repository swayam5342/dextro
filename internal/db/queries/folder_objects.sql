-- name: GetFolderObjectForOwner :one
SELECT o.object_id
FROM folder_objects o
JOIN folders f ON f.id = o.folder_id
WHERE f.user_id = $1 AND o.folder_id = $2 AND o.connection_id = $3;

-- name: SetFolderObject :exec
INSERT INTO folder_objects (folder_id, connection_id, object_id)
VALUES ($1, $2, $3)
ON CONFLICT (folder_id, connection_id) DO UPDATE
SET object_id = EXCLUDED.object_id,
    stale = false;

-- name: AddPendingFolderObject :exec
INSERT INTO folder_objects (folder_id, connection_id)
VALUES ($1, $2)
ON CONFLICT (folder_id, connection_id) DO NOTHING;

-- name: AddPendingFolderObjectsForConnection :exec
INSERT INTO folder_objects (folder_id, connection_id)
SELECT f.id, sqlc.arg(connection_id)::uuid
FROM folders f
WHERE f.user_id = sqlc.arg(user_id)::uuid
ON CONFLICT (folder_id, connection_id) DO NOTHING;

-- name: ListFolderObjectsForConnection :many
SELECT o.folder_id, o.object_id, o.stale
FROM folder_objects o
JOIN folders f ON f.id = o.folder_id
WHERE f.user_id = $1 AND o.connection_id = $2;

-- name: ListFolderObjectsForFolder :many
SELECT o.connection_id, o.object_id
FROM folder_objects o
JOIN folders f ON f.id = o.folder_id
WHERE f.user_id = $1 AND o.folder_id = $2 AND o.object_id IS NOT NULL;

-- name: MarkFolderObjectStale :exec
UPDATE folder_objects SET stale = true WHERE folder_id = $1 AND connection_id = $2;

-- name: ClearFolderObjectStale :exec
UPDATE folder_objects SET stale = false WHERE folder_id = $1 AND connection_id = $2;

-- name: DeleteFolderObject :exec
DELETE FROM folder_objects WHERE folder_id = $1 AND connection_id = $2;

-- name: DeleteFolderIfUnused :execrows
DELETE FROM folders f
WHERE f.user_id = $1 AND f.id = $2
  AND NOT EXISTS (SELECT 1 FROM files x WHERE x.folder_id = f.id)
  AND NOT EXISTS (SELECT 1 FROM folders c WHERE c.parent_id = f.id)
  AND NOT EXISTS (SELECT 1 FROM folder_objects o WHERE o.folder_id = f.id);

-- name: FindFolderByName :one
SELECT *
FROM folders
WHERE user_id = sqlc.arg(user_id)
  AND parent_id IS NOT DISTINCT FROM sqlc.narg(parent_id)::uuid
  AND name = sqlc.arg(name)
ORDER BY created_at, id
LIMIT 1;

-- name: GetConnectionFoldersSynced :one
SELECT (folders_synced_at IS NOT NULL)::boolean AS synced
FROM storage_connections
WHERE user_id = $1 AND id = $2;

-- name: SetConnectionFoldersSynced :exec
UPDATE storage_connections SET folders_synced_at = now() WHERE user_id = $1 AND id = $2;
