-- +goose Up
-- A dextro folder's real folder in one account. A file is whole in one account, so
-- a folder holding files from several has a real folder in each.
CREATE TABLE folder_objects (
    folder_id     uuid NOT NULL REFERENCES folders (id) ON DELETE CASCADE,
    connection_id uuid NOT NULL REFERENCES storage_connections (id) ON DELETE CASCADE,
    -- NULL while it's waiting to be made. Having no row at all means the account has
    -- no folder and shouldn't get one, because someone deleted it in Drive.
    object_id     text,
    -- A rename or move that hasn't reached this account yet.
    stale         boolean NOT NULL DEFAULT false,
    PRIMARY KEY (folder_id, connection_id)
);

CREATE UNIQUE INDEX folder_objects_connection_object_idx ON folder_objects (connection_id, object_id);

-- When a sync first sorted this account's files into real folders.
ALTER TABLE storage_connections ADD COLUMN folders_synced_at timestamptz;

-- +goose Down
ALTER TABLE storage_connections DROP COLUMN folders_synced_at;
DROP TABLE folder_objects;
