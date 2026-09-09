CREATE TABLE pgbase_meta.admin_totp (
    administrator_id uuid PRIMARY KEY REFERENCES pgbase_meta.admins(id) ON DELETE CASCADE,
    secret_ciphertext bytea NOT NULL CHECK (octet_length(secret_ciphertext) BETWEEN 29 AND 256),
    enabled_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE pgbase_meta.admin_ip_allowlist (
    administrator_id uuid NOT NULL REFERENCES pgbase_meta.admins(id) ON DELETE CASCADE,
    prefix inet NOT NULL,
    PRIMARY KEY (administrator_id, prefix)
);
