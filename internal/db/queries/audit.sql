-- name: InsertAuditEvent :exec
INSERT INTO audit_events (actor_id, action, ip, metadata)
VALUES ($1, $2, $3, $4);

-- Instance-wide on purpose: only the admin's audit log calls this.
-- name: ListAuditEvents :many
SELECT e.id, e.action, e.ip, e.metadata, e.created_at, u.email AS actor_email
FROM audit_events e
LEFT JOIN users u ON u.id = e.actor_id
WHERE (sqlc.narg('action')::text IS NULL OR e.action = sqlc.narg('action'))
  AND (sqlc.narg('before')::bigint IS NULL OR e.id < sqlc.narg('before'))
ORDER BY e.id DESC
LIMIT sqlc.arg('page_size');
