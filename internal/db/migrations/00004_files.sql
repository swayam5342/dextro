-- +goose Up
CREATE TABLE api_keys (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         text NOT NULL,
    token_hash   bytea NOT NULL UNIQUE,
    -- The start of the key, so its owner can tell keys apart. Not enough to use it.
    prefix       text NOT NULL,
    scopes       text[] NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz
);

CREATE INDEX api_keys_user_id_idx ON api_keys (user_id);

-- Folders exist only here. Objects sit flat in the storage account, so renaming
-- and moving never touch the provider.
CREATE TABLE folders (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    parent_id  uuid REFERENCES folders (id) ON DELETE CASCADE,
    name       text NOT NULL,
    -- Set on the folder that was trashed, not on what's inside it.
    trashed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX folders_user_parent_idx ON folders (user_id, parent_id);

CREATE TABLE files (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- Removing a connection forgets its files here; they stay in the account.
    connection_id uuid NOT NULL REFERENCES storage_connections (id) ON DELETE CASCADE,
    folder_id     uuid REFERENCES folders (id) ON DELETE CASCADE,
    -- The provider's ID for the object. Never sent to clients.
    object_id     text NOT NULL,
    name          text NOT NULL,
    size          bigint NOT NULL,
    content_type  text NOT NULL,
    trashed_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX files_user_folder_idx ON files (user_id, folder_id);
CREATE INDEX files_connection_id_idx ON files (connection_id);

-- +goose Down
DROP TABLE files;
DROP TABLE folders;
DROP TABLE api_keys;
