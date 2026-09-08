CREATE TABLE pgbase_meta.auth_collections (
    id uuid PRIMARY KEY,
    name text NOT NULL UNIQUE,
    registration_enabled boolean NOT NULL DEFAULT false,
    profile_schema jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(profile_schema) = 'object'),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (octet_length(name) BETWEEN 1 AND 64)
);

INSERT INTO pgbase_meta.auth_collections(id,name,registration_enabled,created_at,updated_at)
VALUES ('00000000-0000-0000-0000-000000000001','users',true,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);

ALTER TABLE pgbase_meta.users ADD COLUMN auth_collection_id uuid
    REFERENCES pgbase_meta.auth_collections(id) ON DELETE RESTRICT;
UPDATE pgbase_meta.users SET auth_collection_id='00000000-0000-0000-0000-000000000001';
ALTER TABLE pgbase_meta.users ALTER COLUMN auth_collection_id SET NOT NULL;
ALTER TABLE pgbase_meta.users ALTER COLUMN auth_collection_id SET DEFAULT '00000000-0000-0000-0000-000000000001';
ALTER TABLE pgbase_meta.users DROP CONSTRAINT users_email_key;
ALTER TABLE pgbase_meta.users ADD CONSTRAINT users_auth_collection_email_key UNIQUE(auth_collection_id,email);
CREATE INDEX users_auth_collection_id_id_idx ON pgbase_meta.users(auth_collection_id,id);

ALTER TABLE pgbase_meta.user_oauth_identities ADD COLUMN auth_collection_id uuid
    REFERENCES pgbase_meta.auth_collections(id) ON DELETE RESTRICT;
UPDATE pgbase_meta.user_oauth_identities SET auth_collection_id='00000000-0000-0000-0000-000000000001';
ALTER TABLE pgbase_meta.user_oauth_identities ALTER COLUMN auth_collection_id SET NOT NULL;
ALTER TABLE pgbase_meta.user_oauth_identities DROP CONSTRAINT user_oauth_identities_pkey;
ALTER TABLE pgbase_meta.user_oauth_identities DROP CONSTRAINT user_oauth_identities_user_id_provider_key;
ALTER TABLE pgbase_meta.user_oauth_identities ADD PRIMARY KEY(auth_collection_id,provider,subject);
ALTER TABLE pgbase_meta.user_oauth_identities ADD UNIQUE(user_id,provider);

ALTER TABLE pgbase_meta.user_oauth_states ADD COLUMN auth_collection_id uuid
    NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'
    REFERENCES pgbase_meta.auth_collections(id) ON DELETE RESTRICT;

CREATE TABLE pgbase_meta.user_auth_profiles (
    user_id uuid PRIMARY KEY REFERENCES pgbase_meta.users(id) ON DELETE CASCADE,
    attributes jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(attributes) = 'object'),
    updated_at timestamptz NOT NULL
);
