-- name: GetSetting :one
SELECT key, value, updated_at
FROM settings
WHERE key = $1;

-- name: UpsertSetting :exec
INSERT INTO settings (key, value)
VALUES ($1, $2)
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value,
    updated_at = now();
