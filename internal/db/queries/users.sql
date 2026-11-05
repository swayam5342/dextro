-- name: AnyUsersExist :one
SELECT EXISTS (SELECT 1 FROM users);

-- name: CreateUser :one
INSERT INTO users (email, password_hash, role)
VALUES ($1, $2, $3)
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
    updated_at = now()
WHERE id = $1;

-- name: UpdateUserRoleByEmail :one
UPDATE users
SET role = $2,
    updated_at = now()
WHERE email = $1
RETURNING *;

-- name: GetIdentityUserID :one
SELECT user_id
FROM user_identities
WHERE provider = $1 AND subject = $2;

-- name: CreateIdentity :exec
INSERT INTO user_identities (provider, subject, user_id, email)
VALUES ($1, $2, $3, $4);
