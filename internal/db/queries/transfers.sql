-- A file already waiting keeps its place. One that failed starts again.
-- name: CreateFileTransfer :execrows
INSERT INTO file_transfers (user_id, file_id, source_connection_id, target_connection_id, size)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (file_id) DO UPDATE
SET source_connection_id = EXCLUDED.source_connection_id,
    target_connection_id = EXCLUDED.target_connection_id,
    size = EXCLUDED.size,
    copied = 0,
    status = 'waiting',
    failure = ''
WHERE file_transfers.status = 'failed';

-- Instance-wide on purpose: only the background job calls this.
-- name: NextFileTransfer :one
SELECT *
FROM file_transfers
WHERE status = 'waiting'
ORDER BY created_at, id
LIMIT 1;

-- name: SetFileTransferCopied :execrows
UPDATE file_transfers
SET copied = $2
WHERE id = $1;

-- name: FailFileTransfer :exec
UPDATE file_transfers
SET status = 'failed', failure = $2, copied = 0
WHERE id = $1;

-- name: DeleteFileTransfer :execrows
DELETE FROM file_transfers WHERE id = $1;

-- name: DeleteFileTransferForOwner :execrows
DELETE FROM file_transfers WHERE user_id = $1 AND id = $2;

-- name: DeleteFileTransfersFromConnectionForOwner :execrows
DELETE FROM file_transfers WHERE user_id = $1 AND source_connection_id = $2;

-- name: RetryFileTransfersForOwner :execrows
UPDATE file_transfers
SET status = 'waiting', failure = '', copied = 0
WHERE user_id = $1 AND status = 'failed';

-- name: ListFileTransfersForOwner :many
SELECT t.*, f.name
FROM file_transfers t
JOIN files f ON f.id = t.file_id
WHERE t.user_id = $1
ORDER BY t.status DESC, t.created_at, t.id
LIMIT 100;

-- name: ListWaitingTransferFilesForOwner :many
SELECT file_id FROM file_transfers WHERE user_id = $1 AND status = 'waiting';

-- name: IsFileWaitingToMoveForOwner :one
SELECT EXISTS (SELECT 1 FROM file_transfers WHERE user_id = $1 AND file_id = $2 AND status = 'waiting');

-- name: GetFileTransferTotalsForOwner :one
SELECT count(*) FILTER (WHERE status = 'waiting') AS waiting,
       coalesce(sum(size) FILTER (WHERE status = 'waiting'), 0)::bigint AS waiting_bytes,
       count(*) FILTER (WHERE status = 'failed') AS failed
FROM file_transfers
WHERE user_id = $1;

-- name: ListConnectionFileCountsForOwner :many
SELECT connection_id,
       count(*) FILTER (WHERE NOT external) AS files,
       count(*) FILTER (WHERE external) AS external_files
FROM files
WHERE user_id = $1
GROUP BY connection_id;

-- name: CreatePendingRemoval :exec
INSERT INTO pending_removals (user_id, connection_id, object_id)
VALUES ($1, $2, $3)
ON CONFLICT (connection_id, object_id) DO NOTHING;

-- Instance-wide on purpose: only the background job calls this.
-- name: ListDuePendingRemovals :many
SELECT *
FROM pending_removals
WHERE retry_at <= now()
ORDER BY created_at, id
LIMIT 200;

-- name: DeletePendingRemoval :exec
DELETE FROM pending_removals WHERE id = $1;

-- name: DelayPendingRemoval :exec
UPDATE pending_removals SET retry_at = $2 WHERE id = $1;

-- name: ListPendingRemovalObjectsForOwner :many
SELECT object_id FROM pending_removals WHERE user_id = $1 AND connection_id = $2;

-- name: CountPendingRemovalsForOwner :one
SELECT count(*) FROM pending_removals WHERE user_id = $1;

-- A last look before a delete that can't be undone.
-- name: IsObjectRecorded :one
SELECT EXISTS (SELECT 1 FROM files WHERE connection_id = $1 AND object_id = $2);
