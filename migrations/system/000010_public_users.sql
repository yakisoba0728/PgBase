CREATE TABLE pgbase_meta.users (
    id uuid PRIMARY KEY,
    email text NOT NULL UNIQUE CHECK (email = lower(btrim(email))),
    password_hash text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE pgbase_meta.user_sessions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES pgbase_meta.users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL
);
CREATE INDEX user_sessions_user_id_idx ON pgbase_meta.user_sessions(user_id);
CREATE INDEX user_sessions_expires_at_idx ON pgbase_meta.user_sessions(expires_at);

CREATE TABLE pgbase_meta.user_auth_rate_limits (
    subject_hash bytea PRIMARY KEY CHECK (octet_length(subject_hash) = 32),
    window_started_at timestamptz NOT NULL,
    failures integer NOT NULL CHECK (failures >= 0),
    blocked_until timestamptz,
    updated_at timestamptz NOT NULL
);
