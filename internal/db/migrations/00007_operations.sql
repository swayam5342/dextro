-- +goose Up
-- What the background quota sync last saw. NULL until it has run. Uploads and the
-- Storage page still ask Google directly, so these are only for the admin's view.
ALTER TABLE storage_connections
    ADD COLUMN checked_at timestamptz,
    ADD COLUMN check_ok   boolean,
    ADD COLUMN used_bytes bigint;

-- The last finish of each background job, kept here because river clears out
-- finished jobs on its own schedule.
CREATE TABLE job_runs (
    name        text PRIMARY KEY,
    finished_at timestamptz NOT NULL,
    succeeded   boolean NOT NULL
);

-- +goose Down
DROP TABLE job_runs;
ALTER TABLE storage_connections
    DROP COLUMN used_bytes,
    DROP COLUMN check_ok,
    DROP COLUMN checked_at;
