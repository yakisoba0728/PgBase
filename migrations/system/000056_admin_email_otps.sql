CREATE TABLE pgbase_meta.admin_email_otps (
    id uuid PRIMARY KEY,
    admin_id uuid NOT NULL REFERENCES pgbase_meta.admins(id) ON DELETE CASCADE,
    code_hash bytea NOT NULL CHECK (octet_length(code_hash) = 32),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT current_timestamp
);

CREATE UNIQUE INDEX admin_email_otps_one_active_admin
    ON pgbase_meta.admin_email_otps (admin_id) WHERE consumed_at IS NULL;

CREATE INDEX admin_email_otps_active_expiry_idx
    ON pgbase_meta.admin_email_otps (expires_at) WHERE consumed_at IS NULL;
