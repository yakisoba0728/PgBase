CREATE TABLE pgbase_meta.record_revisions (
    collection_id uuid NOT NULL REFERENCES pgbase_meta.collections(id) ON DELETE CASCADE,
    record_id uuid NOT NULL,
    version bigint NOT NULL CHECK (version >= 1),
    validator bytea NOT NULL CHECK (octet_length(validator) = 32),
    last_event_id uuid NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (collection_id, record_id)
);

CREATE TABLE pgbase_meta.record_audit_events (
    sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
    event_id uuid PRIMARY KEY,
    actor_kind text NOT NULL CHECK (actor_kind IN ('administrator')),
    actor_id uuid NOT NULL,
    request_id text NOT NULL CHECK (octet_length(request_id) BETWEEN 8 AND 128),
    collection_id uuid NOT NULL,
    collection_name text NOT NULL,
    record_id uuid NOT NULL,
    operation text NOT NULL CHECK (operation IN ('create', 'update', 'delete')),
    schema_version bigint NOT NULL CHECK (schema_version >= 0),
    schema_digest bytea NOT NULL CHECK (octet_length(schema_digest) = 32),
    previous_version bigint CHECK (previous_version >= 0),
    new_version bigint NOT NULL CHECK (new_version >= 1),
    previous_etag text,
    new_etag text,
    changed_fields text[] NOT NULL CHECK (array_position(changed_fields, NULL) IS NULL),
    input_hash bytea NOT NULL CHECK (octet_length(input_hash) = 32),
    idempotency_key_hash bytea CHECK (idempotency_key_hash IS NULL OR octet_length(idempotency_key_hash) = 32),
    compatibility text NOT NULL CHECK (compatibility IN ('none', 'missing-idempotency-key', 'missing-if-match')),
    committed_at timestamptz NOT NULL,
    CHECK ((
        (operation = 'create'
            AND previous_version IS NULL AND new_version = 1
            AND previous_etag IS NULL AND new_etag IS NOT NULL
            AND compatibility IN ('none', 'missing-idempotency-key')
            AND ((compatibility = 'none' AND idempotency_key_hash IS NOT NULL)
                OR (compatibility = 'missing-idempotency-key' AND idempotency_key_hash IS NULL)))
        OR (operation = 'update'
            AND previous_version IS NOT NULL AND new_version = previous_version + 1
            AND previous_etag IS NOT NULL AND new_etag IS NOT NULL
            AND cardinality(changed_fields) > 0
            AND idempotency_key_hash IS NULL
            AND compatibility IN ('none', 'missing-if-match'))
        OR (operation = 'delete'
            AND previous_version IS NOT NULL AND new_version = previous_version + 1
            AND previous_etag IS NOT NULL AND new_etag IS NULL
            AND cardinality(changed_fields) = 0
            AND idempotency_key_hash IS NULL
            AND compatibility IN ('none', 'missing-if-match'))
    ) IS TRUE)
);
ALTER TABLE pgbase_meta.record_audit_events
    ADD CONSTRAINT record_audit_events_resource_version_unique
    UNIQUE (collection_id, record_id, new_version);

ALTER TABLE pgbase_meta.record_revisions
    ADD CONSTRAINT record_revisions_last_event_fk
    FOREIGN KEY (last_event_id) REFERENCES pgbase_meta.record_audit_events(event_id)
    DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX record_audit_events_resource_idx
    ON pgbase_meta.record_audit_events (collection_id, record_id, sequence);
CREATE INDEX record_audit_events_actor_idx
    ON pgbase_meta.record_audit_events (actor_kind, actor_id, sequence);

CREATE FUNCTION pgbase_meta.reject_record_audit_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'record audit events are append-only' USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER record_audit_events_append_only
BEFORE UPDATE OR DELETE ON pgbase_meta.record_audit_events
FOR EACH ROW EXECUTE FUNCTION pgbase_meta.reject_record_audit_mutation();

CREATE TRIGGER record_audit_events_reject_truncate
BEFORE TRUNCATE ON pgbase_meta.record_audit_events
FOR EACH STATEMENT EXECUTE FUNCTION pgbase_meta.reject_record_audit_mutation();

CREATE FUNCTION pgbase_meta.validate_record_revision_event()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pgbase_meta.record_audit_events AS event
        WHERE event.event_id = NEW.last_event_id
          AND event.collection_id = NEW.collection_id
          AND event.record_id = NEW.record_id
          AND event.new_version = NEW.version
          AND event.operation IN ('create', 'update')
    ) THEN
        RAISE EXCEPTION 'record revision does not match its audit event' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER record_revisions_validate_event
BEFORE INSERT OR UPDATE ON pgbase_meta.record_revisions
FOR EACH ROW EXECUTE FUNCTION pgbase_meta.validate_record_revision_event();

CREATE TABLE pgbase_meta.record_outbox (
    event_id uuid PRIMARY KEY REFERENCES pgbase_meta.record_audit_events(event_id) ON DELETE RESTRICT,
    topic text NOT NULL CHECK (topic = 'record.mutation.v1'),
    collection_id uuid NOT NULL,
    collection_name text NOT NULL,
    record_id uuid NOT NULL,
    operation text NOT NULL CHECK (operation IN ('create', 'update', 'delete')),
    record_version bigint NOT NULL CHECK (record_version >= 1),
    envelope jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    CHECK ((
        jsonb_typeof(envelope) = 'object'
        AND envelope->>'version' = '1'
        AND envelope->>'eventId' = event_id::text
        AND envelope->>'collectionId' = collection_id::text
        AND envelope->>'collectionName' = collection_name
        AND envelope->>'recordId' = record_id::text
        AND envelope->>'operation' = operation
        AND envelope->>'recordVersion' = record_version::text
        AND CASE WHEN operation = 'delete'
            THEN envelope->>'tombstone' = 'true'
            ELSE NOT (envelope ? 'tombstone')
        END
    ) IS TRUE)
);
CREATE INDEX record_outbox_created_idx ON pgbase_meta.record_outbox (created_at, event_id);

CREATE FUNCTION pgbase_meta.validate_record_outbox_event()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pgbase_meta.record_audit_events AS event
        WHERE event.event_id = NEW.event_id
          AND event.collection_id = NEW.collection_id
          AND event.collection_name = NEW.collection_name
          AND event.record_id = NEW.record_id
          AND event.operation = NEW.operation
          AND event.new_version = NEW.record_version
    ) THEN
        RAISE EXCEPTION 'record outbox row does not match its audit event' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER record_outbox_validate_event
BEFORE INSERT OR UPDATE ON pgbase_meta.record_outbox
FOR EACH ROW EXECUTE FUNCTION pgbase_meta.validate_record_outbox_event();

CREATE TABLE pgbase_meta.record_idempotency (
    actor_kind text NOT NULL CHECK (actor_kind IN ('administrator')),
    actor_id uuid NOT NULL,
    collection_id uuid NOT NULL,
    key_hash bytea NOT NULL CHECK (octet_length(key_hash) = 32),
    request_hash bytea NOT NULL CHECK (octet_length(request_hash) = 32),
    record_id uuid NOT NULL,
    event_id uuid NOT NULL REFERENCES pgbase_meta.record_audit_events(event_id) ON DELETE RESTRICT,
    response_body jsonb NOT NULL,
    etag text NOT NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at),
    CHECK ((jsonb_typeof(response_body) = 'object' AND response_body->>'id' = record_id::text) IS TRUE),
    PRIMARY KEY (actor_kind, actor_id, collection_id, key_hash)
);
ALTER TABLE pgbase_meta.record_idempotency
    ADD CONSTRAINT record_idempotency_event_unique UNIQUE (event_id);
CREATE INDEX record_idempotency_expiry_idx
    ON pgbase_meta.record_idempotency (expires_at);

CREATE FUNCTION pgbase_meta.validate_record_idempotency_event()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pgbase_meta.record_audit_events AS event
        WHERE event.event_id = NEW.event_id
          AND event.actor_kind = NEW.actor_kind
          AND event.actor_id = NEW.actor_id
          AND event.collection_id = NEW.collection_id
          AND event.record_id = NEW.record_id
          AND event.operation = 'create'
          AND event.new_version = 1
          AND event.new_etag = NEW.etag
          AND event.input_hash = NEW.request_hash
          AND event.idempotency_key_hash = NEW.key_hash
    ) THEN
        RAISE EXCEPTION 'record idempotency row does not match its audit event' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER record_idempotency_validate_event
BEFORE INSERT OR UPDATE ON pgbase_meta.record_idempotency
FOR EACH ROW EXECUTE FUNCTION pgbase_meta.validate_record_idempotency_event();
