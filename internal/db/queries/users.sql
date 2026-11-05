-- name: AnyUsersExist :one
SELECT EXISTS (SELECT 1 FROM users);

-- name: CreateUser :one
INSERT INTO users (email, password_hash, role, must_change_password)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetUser :one
SELECT u.*,
       EXISTS (
           SELECT 1 FROM user_identities i
           WHERE i.user_id = u.id AND i.provider = 'google'
       ) AS google_linked
FROM users u
WHERE u.id = $1;

-- Sign-in is the one place a user is found by what they typed.
-- name: GetUserForLogin :one
SELECT * FROM users WHERE email = $1;

-- name: UpdateUserPassword :execrows
UPDATE users
SET password_hash = $2,
    must_change_password = false,
    updated_at = now()
WHERE id = $1;

-- The admin chose this one, so the user has to replace it.
-- name: SetTemporaryPassword :execrows
UPDATE users
SET password_hash = $2,
    must_change_password = true,
    updated_at = now()
WHERE id = $1;

-- Instance-wide on purpose: only the admin's user list calls this.
-- name: ListUsersForAdmin :many
SELECT u.id, u.email, u.role, u.password_hash, u.must_change_password, u.created_at,
       EXISTS (
           SELECT 1 FROM user_identities i
           WHERE i.user_id = u.id AND i.provider = 'google'
       ) AS google_linked,
       (SELECT count(*) FROM storage_connections c WHERE c.user_id = u.id) AS linked_accounts
FROM users u
ORDER BY u.created_at, u.id;

-- name: DeleteUserForAdmin :execrows
DELETE FROM users WHERE id = $1;

-- Demote and promote run in one transaction, because only one admin can exist.
-- name: DemoteAdmin :exec
UPDATE users SET role = 'user', updated_at = now() WHERE role = 'admin';

-- name: PromoteUserByEmail :execrows
UPDATE users SET role = 'admin', updated_at = now() WHERE email = $1;

-- name: GetUserIDByEmail :one
SELECT id FROM users WHERE email = $1;

-- name: GetIdentityUserID :one
SELECT user_id
FROM user_identities
WHERE provider = $1 AND subject = $2;

-- name: CreateIdentity :exec
INSERT INTO user_identities (provider, subject, user_id, email)
VALUES ($1, $2, $3, $4);

-- Serializes account creation so exactly one sign-up can be the first.
-- name: LockUserCreation :exec
SELECT pg_advisory_xact_lock(hashtextextended('dextro.user_creation', 0));
