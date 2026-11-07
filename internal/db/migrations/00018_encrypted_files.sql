-- +goose Up
-- New files anywhere under an encrypted folder are encrypted on their way to the account.
-- Files already there stay as they are.
ALTER TABLE folders ADD COLUMN encrypted boolean NOT NULL DEFAULT false;

-- Each file has its own key, sealed by the server's keyring. A copy of it travels in the
-- object, so that a sync can bring the file back after losing this table.
ALTER TABLE files ADD COLUMN encrypted boolean NOT NULL DEFAULT false;
ALTER TABLE files ADD COLUMN key_id text;
ALTER TABLE files ADD COLUMN key_ciphertext bytea;
ALTER TABLE files ADD CONSTRAINT files_encrypted_has_key CHECK (NOT encrypted OR key_ciphertext IS NOT NULL);

ALTER TABLE upload_sessions ADD COLUMN encrypted boolean NOT NULL DEFAULT false;
ALTER TABLE upload_sessions ADD COLUMN key_id text;
ALTER TABLE upload_sessions ADD COLUMN key_ciphertext bytea;
-- The running hash of the file's real content, which the account can't report once it only
-- sees encrypted bytes. Empty means it was lost, and the file just can't match duplicates.
ALTER TABLE upload_sessions ADD COLUMN hash_state bytea;

-- +goose Down
ALTER TABLE upload_sessions DROP COLUMN hash_state;
ALTER TABLE upload_sessions DROP COLUMN key_ciphertext;
ALTER TABLE upload_sessions DROP COLUMN key_id;
ALTER TABLE upload_sessions DROP COLUMN encrypted;
ALTER TABLE files DROP CONSTRAINT files_encrypted_has_key;
ALTER TABLE files DROP COLUMN key_ciphertext;
ALTER TABLE files DROP COLUMN key_id;
ALTER TABLE files DROP COLUMN encrypted;
ALTER TABLE folders DROP COLUMN encrypted;
