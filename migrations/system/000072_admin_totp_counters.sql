ALTER TABLE pgbase_meta.admin_totp
    ADD COLUMN last_counter bigint CHECK (last_counter >= 0);
