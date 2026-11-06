-- +goose Up
-- A file waiting to move between two of a user's accounts. While it's waiting, the row
-- also holds its size on the target, so that nothing else counts on the same room. A
-- row that failed stays so its owner can see why, and holds nothing.
CREATE TABLE file_transfers (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id              uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    file_id              uuid NOT NULL UNIQUE REFERENCES files (id) ON DELETE CASCADE,
    source_connection_id uuid NOT NULL REFERENCES storage_connections (id) ON DELETE CASCADE,
    target_connection_id uuid NOT NULL REFERENCES storage_connections (id) ON DELETE CASCADE,
    size                 bigint NOT NULL,
    copied               bigint NOT NULL DEFAULT 0,
    status               text NOT NULL DEFAULT 'waiting' CHECK (status IN ('waiting', 'failed')),
    failure              text NOT NULL DEFAULT '',
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX file_transfers_user_id_idx ON file_transfers (user_id);
CREATE INDEX file_transfers_waiting_idx ON file_transfers (created_at, id) WHERE status = 'waiting';

-- A copy left behind by a finished move, still to be deleted from its old account. It
-- has no record any more, so without this a sync would bring it back as a new file. It
-- isn't tied to the file: deleting the file doesn't change that the old copy has to go.
CREATE TABLE pending_removals (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    connection_id uuid NOT NULL REFERENCES storage_connections (id) ON DELETE CASCADE,
    object_id     text NOT NULL,
    retry_at      timestamptz NOT NULL DEFAULT now(),
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (connection_id, object_id)
);

CREATE INDEX pending_removals_user_id_idx ON pending_removals (user_id);
CREATE INDEX pending_removals_retry_at_idx ON pending_removals (retry_at);

-- An account being emptied. New files stay out of it, even though it's the one with
-- the most room.
ALTER TABLE storage_connections ADD COLUMN draining boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE storage_connections DROP COLUMN draining;
DROP TABLE pending_removals;
DROP TABLE file_transfers;
