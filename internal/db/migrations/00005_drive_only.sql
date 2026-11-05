-- +goose Up
-- S3 was removed. File records go with their connections through the foreign
-- key; the objects themselves stay in the bucket.
DELETE FROM storage_connections WHERE provider <> 'google_drive';

ALTER TABLE storage_connections DROP CONSTRAINT storage_connections_provider_check;
ALTER TABLE storage_connections ADD CONSTRAINT storage_connections_provider_check
    CHECK (provider = 'google_drive');

-- +goose Down
ALTER TABLE storage_connections DROP CONSTRAINT storage_connections_provider_check;
ALTER TABLE storage_connections ADD CONSTRAINT storage_connections_provider_check
    CHECK (provider IN ('s3', 'google_drive'));
