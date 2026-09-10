ALTER TABLE pgbase_meta.collections
    ADD COLUMN record_id jsonb NOT NULL DEFAULT '{"kind":"uuid"}'::jsonb,
    ADD CONSTRAINT collections_record_id_object
        CHECK (jsonb_typeof(record_id) = 'object'),
    ADD CONSTRAINT collections_record_id_kind
        CHECK (record_id ? 'kind' AND record_id->>'kind' IN ('uuid', 'text'));

ALTER TABLE pgbase_meta.auth_collections
    ADD COLUMN record_id jsonb NOT NULL DEFAULT '{"kind":"uuid"}'::jsonb,
    ADD CONSTRAINT auth_collections_record_id_object
        CHECK (jsonb_typeof(record_id) = 'object'),
    ADD CONSTRAINT auth_collections_record_id_kind
        CHECK (record_id ? 'kind' AND record_id->>'kind' IN ('uuid', 'text'));

-- Both collection families own record identities. This registry gives the
-- mapping table one real foreign key while retaining the separate catalog
-- tables used by PgBase. The all-zero scope is reserved for PocketBase's
-- cross-auth-collection ID uniqueness rule.
CREATE TABLE pgbase_meta.record_identity_collections (
    id uuid PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('base', 'auth')),
    scope_id uuid NOT NULL,
    UNIQUE (id, kind, scope_id),
    CHECK ((kind = 'base' AND scope_id = id AND id <> '00000000-0000-0000-0000-000000000000')
        OR (kind = 'auth' AND scope_id = '00000000-0000-0000-0000-000000000000'))
);

INSERT INTO pgbase_meta.record_identity_collections(id,kind,scope_id)
SELECT id,'base',id FROM pgbase_meta.collections
UNION ALL
SELECT id,'auth','00000000-0000-0000-0000-000000000000' FROM pgbase_meta.auth_collections;

CREATE TABLE pgbase_meta.record_identities (
    collection_id uuid NOT NULL,
    collection_kind text NOT NULL,
    scope_id uuid NOT NULL,
    internal_id uuid NOT NULL,
    external_id text COLLATE "C" NOT NULL,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    PRIMARY KEY (collection_id, internal_id),
    UNIQUE (scope_id, external_id),
    FOREIGN KEY (collection_id, collection_kind, scope_id)
        REFERENCES pgbase_meta.record_identity_collections(id, kind, scope_id)
        ON DELETE CASCADE,
    CONSTRAINT record_identities_external_id_length
        CHECK (char_length(external_id) BETWEEN 1 AND 255)
);

-- Existing auth UUIDs participate in the same authority as future text IDs,
-- preventing a text ID in one realm from shadowing a UUID ID in another.
INSERT INTO pgbase_meta.record_identities
    (collection_id,collection_kind,scope_id,internal_id,external_id)
SELECT auth_collection_id,'auth','00000000-0000-0000-0000-000000000000',id,id::text
FROM pgbase_meta.users;

CREATE FUNCTION pgbase_meta.sync_record_identity_collection() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pgbase_meta AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO pgbase_meta.record_identity_collections(id,kind,scope_id)
        VALUES (NEW.id,TG_ARGV[0],CASE WHEN TG_ARGV[0]='auth'
            THEN '00000000-0000-0000-0000-000000000000'::uuid ELSE NEW.id END);
        RETURN NEW;
    END IF;
    DELETE FROM pgbase_meta.record_identity_collections WHERE id=OLD.id AND kind=TG_ARGV[0];
    RETURN OLD;
END $$;

CREATE TRIGGER collections_record_identity_owner_insert
AFTER INSERT ON pgbase_meta.collections FOR EACH ROW
EXECUTE FUNCTION pgbase_meta.sync_record_identity_collection('base');
CREATE TRIGGER collections_record_identity_owner_delete
BEFORE DELETE ON pgbase_meta.collections FOR EACH ROW
EXECUTE FUNCTION pgbase_meta.sync_record_identity_collection('base');
CREATE TRIGGER auth_collections_record_identity_owner_insert
AFTER INSERT ON pgbase_meta.auth_collections FOR EACH ROW
EXECUTE FUNCTION pgbase_meta.sync_record_identity_collection('auth');
CREATE TRIGGER auth_collections_record_identity_owner_delete
BEFORE DELETE ON pgbase_meta.auth_collections FOR EACH ROW
EXECUTE FUNCTION pgbase_meta.sync_record_identity_collection('auth');

CREATE FUNCTION pgbase_meta.require_auth_record_identity() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pgbase_meta AS $$
DECLARE policy_kind text;
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pgbase_meta.record_identities i
        WHERE i.collection_kind='auth' AND i.collection_id=NEW.auth_collection_id
          AND i.internal_id=NEW.id
    ) THEN
        SELECT record_id->>'kind' INTO policy_kind
        FROM pgbase_meta.auth_collections WHERE id=NEW.auth_collection_id;
        IF policy_kind='uuid' THEN
            INSERT INTO pgbase_meta.record_identities
                (collection_id,collection_kind,scope_id,internal_id,external_id)
            VALUES (NEW.auth_collection_id,'auth','00000000-0000-0000-0000-000000000000',NEW.id,NEW.id::text);
        END IF;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pgbase_meta.record_identities i
        WHERE i.collection_kind='auth' AND i.collection_id=NEW.auth_collection_id
          AND i.internal_id=NEW.id
    ) THEN
        RAISE EXCEPTION 'auth record identity missing' USING ERRCODE='23503';
    END IF;
    RETURN NULL;
END $$;

CREATE CONSTRAINT TRIGGER users_require_record_identity
AFTER INSERT OR UPDATE OF id,auth_collection_id ON pgbase_meta.users
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW
EXECUTE FUNCTION pgbase_meta.require_auth_record_identity();

CREATE FUNCTION pgbase_meta.remove_auth_record_identity() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pgbase_meta AS $$
BEGIN
    DELETE FROM pgbase_meta.record_identities
    WHERE collection_kind='auth' AND collection_id=OLD.auth_collection_id AND internal_id=OLD.id;
    RETURN OLD;
END $$;

CREATE TRIGGER users_remove_record_identity
AFTER DELETE ON pgbase_meta.users FOR EACH ROW
EXECUTE FUNCTION pgbase_meta.remove_auth_record_identity();

REVOKE ALL ON pgbase_meta.record_identity_collections FROM PUBLIC;
REVOKE ALL ON pgbase_meta.record_identities FROM PUBLIC;
