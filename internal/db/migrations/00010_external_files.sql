-- +goose Up
-- True for someone's own Drive file that they added to dextro, as opposed to a file
-- dextro stored. Deleting one removes dextro's record and leaves the file alone.
ALTER TABLE files ADD COLUMN external boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE files DROP COLUMN external;
