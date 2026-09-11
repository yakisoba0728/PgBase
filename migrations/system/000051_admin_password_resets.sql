CREATE TABLE pgbase_meta.admin_password_resets (
    token_hash bytea PRIMARY KEY,
    admin_id uuid NOT NULL REFERENCES pgbase_meta.admins(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz
);

CREATE INDEX admin_password_resets_active_expiry_idx
    ON pgbase_meta.admin_password_resets (expires_at)
    WHERE consumed_at IS NULL;
