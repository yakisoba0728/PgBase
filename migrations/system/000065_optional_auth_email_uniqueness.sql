ALTER TABLE pgbase_meta.users DROP CONSTRAINT users_auth_collection_email_key;
CREATE UNIQUE INDEX users_auth_collection_email_nonempty_key
    ON pgbase_meta.users(auth_collection_id,email)
    WHERE email <> '';
