-- +goose Up
-- It was written on every change and never read.
ALTER TABLE file_transfers DROP COLUMN updated_at;

-- +goose Down
ALTER TABLE file_transfers ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
