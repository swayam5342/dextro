-- name: InsertAuditEvent :exec
INSERT INTO audit_events (actor_id, action, ip, metadata)
VALUES ($1, $2, $3, $4);
