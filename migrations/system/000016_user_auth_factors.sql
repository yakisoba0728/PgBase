ALTER TABLE pgbase_meta.users
    ADD COLUMN password_enabled boolean NOT NULL DEFAULT true;

CREATE TABLE pgbase_meta.user_oauth_identities (
    provider text NOT NULL,
    subject text NOT NULL,
    user_id uuid NOT NULL REFERENCES pgbase_meta.users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (provider, subject),
    UNIQUE (user_id, provider),
    CHECK (octet_length(provider) BETWEEN 1 AND 64),
    CHECK (octet_length(subject) BETWEEN 1 AND 512)
);

CREATE TABLE pgbase_meta.user_oauth_states (
    id uuid PRIMARY KEY,
    provider text NOT NULL,
    state_hash bytea NOT NULL UNIQUE CHECK (octet_length(state_hash) = 32),
    verifier_ciphertext bytea NOT NULL,
    redirect_uri text NOT NULL,
    link_user_id uuid REFERENCES pgbase_meta.users(id) ON DELETE CASCADE,
    credential_hash bytea CHECK (credential_hash IS NULL OR octet_length(credential_hash) = 32),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL,
    CHECK ((link_user_id IS NULL) = (credential_hash IS NULL))
);
CREATE INDEX user_oauth_states_expires_at_idx ON pgbase_meta.user_oauth_states(expires_at);

CREATE TABLE pgbase_meta.user_totp_factors (
    user_id uuid PRIMARY KEY REFERENCES pgbase_meta.users(id) ON DELETE CASCADE,
    secret_ciphertext bytea NOT NULL,
    active boolean NOT NULL DEFAULT false,
    last_counter bigint,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE pgbase_meta.user_totp_recovery_codes (
    user_id uuid NOT NULL REFERENCES pgbase_meta.users(id) ON DELETE CASCADE,
    code_hash bytea NOT NULL CHECK (octet_length(code_hash) = 32),
    consumed_at timestamptz,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, code_hash)
);

CREATE TABLE pgbase_meta.user_mfa_challenges (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES pgbase_meta.users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL
);
CREATE INDEX user_mfa_challenges_expires_at_idx ON pgbase_meta.user_mfa_challenges(expires_at);
