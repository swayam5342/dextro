-- +goose Up
-- The sync adds files it finds in an account, so this is what stops two syncs at
-- once from adding the same one twice.
CREATE UNIQUE INDEX files_connection_object_idx ON files (connection_id, object_id);

-- +goose Down
DROP INDEX files_connection_object_idx;
