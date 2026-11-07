-- +goose Up
-- checksum is what the account says about the stored object, which is what a check compares.
-- content_hash identifies what's in the file, which is how duplicates are found. They're the
-- same unless the stored object isn't the file's own bytes. Either can be empty: Google Docs
-- have no checksum, and older files get theirs from the next sync.
ALTER TABLE files ADD COLUMN checksum text;
ALTER TABLE files ADD COLUMN content_hash text;
-- When a check last tried this file, whatever happened, so that one file can't hold up the rest.
ALTER TABLE files ADD COLUMN checked_at timestamptz;

CREATE INDEX files_content_hash_idx ON files (user_id, content_hash, size) WHERE content_hash IS NOT NULL;
CREATE INDEX files_check_order_idx ON files (checked_at NULLS FIRST, id) WHERE checksum IS NOT NULL AND NOT external;

-- What's wrong with a file, kept until its owner dismisses it. A missing file has no record
-- any more, so this keeps its name and the row has no file_id.
CREATE TABLE file_problems (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    file_id           uuid REFERENCES files (id) ON DELETE CASCADE,
    connection_id     uuid NOT NULL REFERENCES storage_connections (id) ON DELETE CASCADE,
    name              text NOT NULL,
    size              bigint NOT NULL,
    kind              text NOT NULL CHECK (kind IN ('missing', 'changed', 'damaged')),
    -- What the account says now, for a file that changed. Dismissing accepts it.
    reported_checksum text,
    found_at          timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX file_problems_file_kind_idx ON file_problems (file_id, kind) WHERE file_id IS NOT NULL;
CREATE INDEX file_problems_user_idx ON file_problems (user_id, found_at DESC);

-- +goose Down
DROP TABLE file_problems;
DROP INDEX files_check_order_idx;
DROP INDEX files_content_hash_idx;
ALTER TABLE files DROP COLUMN checked_at;
ALTER TABLE files DROP COLUMN content_hash;
ALTER TABLE files DROP COLUMN checksum;
