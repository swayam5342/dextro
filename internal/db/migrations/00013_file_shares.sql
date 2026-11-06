-- +goose Up
-- A public Google link on one file. The row is written before Google is asked, so a
-- crash in between can't leave a public link nothing knows about: url stays empty until
-- Google has confirmed, and the expiry job clears up rows left like that.
CREATE TABLE file_shares (
    file_id    uuid PRIMARY KEY REFERENCES files (id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role       text NOT NULL CHECK (role IN ('viewer', 'commenter', 'editor')),
    url        text NOT NULL DEFAULT '',
    -- Google has no expiry for links like this, so dextro removes them itself.
    expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX file_shares_user_id_idx ON file_shares (user_id);
CREATE INDEX file_shares_expires_at_idx ON file_shares (expires_at) WHERE expires_at IS NOT NULL;

-- +goose Down
DROP TABLE file_shares;
