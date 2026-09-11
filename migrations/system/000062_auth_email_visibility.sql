-- A record's public email disclosure choice is independent from collection
-- view rules. Existing accounts preserve PocketBase's safe default (hidden).
ALTER TABLE pgbase_meta.users
    ADD COLUMN email_visibility boolean NOT NULL DEFAULT false;
