CREATE TABLE pgbase_meta.auth_token_policies (
  auth_collection_id uuid NOT NULL REFERENCES pgbase_meta.auth_collections(id) ON DELETE CASCADE,
  purpose text NOT NULL CHECK (purpose IN ('verify','password_reset','email_change','session','otp','impersonation')),
  ttl_seconds integer NOT NULL CHECK (ttl_seconds BETWEEN 60 AND 86400),
  key_version text NOT NULL CHECK (octet_length(key_version) BETWEEN 1 AND 32),
  key_ciphertext bytea NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (auth_collection_id,purpose),
  UNIQUE(key_version)
);
