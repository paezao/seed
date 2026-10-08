-- Browsers my owner has signed in with. Only hashes are stored: id is
-- sha256 of the browser's cookie secret, api_hash sha256 of the API token
-- derived from it.
CREATE TABLE owner_sessions (
    id           text PRIMARY KEY,
    api_hash     text NOT NULL UNIQUE,
    label        text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);
