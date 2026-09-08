CREATE TABLE pgbase_meta.auth_collection_policies (
    auth_collection_id uuid PRIMARY KEY REFERENCES pgbase_meta.auth_collections(id) ON DELETE CASCADE,
    password_enabled boolean NOT NULL DEFAULT true,
    minimum_password_bytes integer NOT NULL DEFAULT 12 CHECK (minimum_password_bytes BETWEEN 12 AND 128),
    mfa_enabled boolean NOT NULL DEFAULT true,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO pgbase_meta.auth_collection_policies(auth_collection_id)
SELECT id FROM pgbase_meta.auth_collections;

CREATE TABLE pgbase_meta.auth_oauth_providers (
    auth_collection_id uuid NOT NULL REFERENCES pgbase_meta.auth_collections(id) ON DELETE CASCADE,
    name text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    authorization_url text NOT NULL,
    token_url text NOT NULL,
    user_info_url text NOT NULL,
    client_id text NOT NULL,
    client_secret_ciphertext bytea,
    scopes jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(scopes) = 'array'),
    redirect_uris jsonb NOT NULL CHECK (jsonb_typeof(redirect_uris) = 'array'),
    subject_field text NOT NULL DEFAULT 'sub',
    email_field text NOT NULL DEFAULT 'email',
    email_verified_field text NOT NULL DEFAULT 'email_verified',
    allow_insecure_loopback boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(auth_collection_id,name),
    CHECK (octet_length(name) BETWEEN 1 AND 64)
);

CREATE TABLE pgbase_meta.auth_mail_templates (
    auth_collection_id uuid NOT NULL REFERENCES pgbase_meta.auth_collections(id) ON DELETE CASCADE,
    purpose text NOT NULL CHECK (purpose IN ('verify','password_reset','email_change')),
    subject text NOT NULL CHECK (octet_length(subject) BETWEEN 1 AND 200),
    body text NOT NULL CHECK (octet_length(body) BETWEEN 1 AND 16384),
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(auth_collection_id,purpose)
);

CREATE TABLE pgbase_meta.auth_smtp_configs (
    auth_collection_id uuid PRIMARY KEY REFERENCES pgbase_meta.auth_collections(id) ON DELETE CASCADE,
    address text NOT NULL,
    sender text NOT NULL,
    username text NOT NULL DEFAULT '',
    password_ciphertext bytea,
    server_name text NOT NULL DEFAULT '',
    implicit_tls boolean NOT NULL DEFAULT false,
    allow_insecure_loopback boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE FUNCTION pgbase_meta.create_auth_policy() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO pgbase_meta.auth_collection_policies(auth_collection_id) VALUES (NEW.id);
    RETURN NEW;
END $$;
CREATE TRIGGER auth_collection_policy_after_insert AFTER INSERT ON pgbase_meta.auth_collections
FOR EACH ROW EXECUTE FUNCTION pgbase_meta.create_auth_policy();
