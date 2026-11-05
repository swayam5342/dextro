-- name: RecordJobRun :exec
INSERT INTO job_runs (name, finished_at, succeeded)
VALUES ($1, now(), $2)
ON CONFLICT (name) DO UPDATE
SET finished_at = EXCLUDED.finished_at,
    succeeded = EXCLUDED.succeeded;

-- name: ListJobRuns :many
SELECT * FROM job_runs ORDER BY name;
