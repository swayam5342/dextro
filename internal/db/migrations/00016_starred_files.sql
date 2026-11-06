-- +goose Up
ALTER TABLE files ADD COLUMN starred_at timestamptz;

CREATE INDEX files_user_starred_idx ON files (user_id, starred_at) WHERE starred_at IS NOT NULL;

-- +goose Down
DROP INDEX files_user_starred_idx;
ALTER TABLE files DROP COLUMN starred_at;
