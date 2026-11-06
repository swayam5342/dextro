-- +goose Up
-- How a user's uploads choose an account. No row means the most free space.
CREATE TABLE upload_routing (
    user_id uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    mode text NOT NULL CHECK (mode IN ('most_space', 'round_robin', 'priority')),
    -- The order the user set. Accounts missing from it come after, oldest first.
    account_order uuid[] NOT NULL DEFAULT '{}',
    -- The account round robin used last.
    last_connection_id uuid REFERENCES storage_connections (id) ON DELETE SET NULL
);

-- +goose Down
DROP TABLE upload_routing;
