-- +goose Up
-- There is one admin. If an instance has several, the oldest stays and the
-- change is recorded, because nobody asked for it through the usual command.
WITH keep AS (
    SELECT id FROM users WHERE role = 'admin' ORDER BY created_at, id LIMIT 1
), demoted AS (
    UPDATE users
    SET role = 'user', updated_at = now()
    WHERE role = 'admin' AND id NOT IN (SELECT id FROM keep)
    RETURNING email
)
INSERT INTO audit_events (action, ip, metadata)
SELECT 'user.role_changed', 'migration', jsonb_build_object('email', email, 'role', 'user')
FROM demoted;

CREATE UNIQUE INDEX users_one_admin ON users ((true)) WHERE role = 'admin';

-- Set when someone else chose the password, so it has to be replaced.
ALTER TABLE users ADD COLUMN must_change_password boolean NOT NULL DEFAULT false;

-- Sign-up closes after the first account, so this no longer means anything.
DELETE FROM settings WHERE key = 'allow_registration';

-- +goose Down
ALTER TABLE users DROP COLUMN must_change_password;
DROP INDEX users_one_admin;
