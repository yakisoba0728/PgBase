CREATE TABLE pgbase_meta.collection_access (
    collection_id uuid PRIMARY KEY REFERENCES pgbase_meta.collections(id) ON DELETE CASCADE,
    enabled boolean NOT NULL DEFAULT false
);

CREATE TABLE pgbase_meta.record_owners (
    collection_id uuid NOT NULL,
    record_id uuid NOT NULL,
    user_id uuid NOT NULL REFERENCES pgbase_meta.users(id) ON DELETE RESTRICT,
    PRIMARY KEY (collection_id, record_id),
    FOREIGN KEY (collection_id, record_id)
        REFERENCES pgbase_meta.record_revisions(collection_id, record_id) ON DELETE CASCADE
);
CREATE INDEX record_owners_user_collection_idx
    ON pgbase_meta.record_owners (user_id, collection_id, record_id);

ALTER TABLE pgbase_meta.record_audit_events
    DROP CONSTRAINT record_audit_events_actor_kind_check,
    ADD CONSTRAINT record_audit_events_actor_kind_check CHECK (actor_kind IN ('administrator', 'user'));

ALTER TABLE pgbase_meta.record_idempotency
    DROP CONSTRAINT record_idempotency_actor_kind_check,
    ADD CONSTRAINT record_idempotency_actor_kind_check CHECK (actor_kind IN ('administrator', 'user'));
