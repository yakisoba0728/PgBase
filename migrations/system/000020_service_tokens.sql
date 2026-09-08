CREATE TABLE pgbase_meta.service_tokens (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    token_hash bytea NOT NULL UNIQUE CHECK(octet_length(token_hash)=32),
    active boolean NOT NULL DEFAULT true,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL,
    CHECK(octet_length(name) BETWEEN 1 AND 128)
);
CREATE INDEX service_tokens_expires_at_idx ON pgbase_meta.service_tokens(expires_at);

CREATE TABLE pgbase_meta.service_token_scopes (
    token_id uuid NOT NULL REFERENCES pgbase_meta.service_tokens(id) ON DELETE CASCADE,
    collection_id uuid NOT NULL REFERENCES pgbase_meta.collections(id) ON DELETE RESTRICT,
    action text NOT NULL CHECK(action IN ('read','create','update','delete')),
    PRIMARY KEY(token_id,collection_id,action)
);

ALTER TABLE pgbase_meta.record_audit_events DROP CONSTRAINT record_audit_events_actor_kind_check;
ALTER TABLE pgbase_meta.record_audit_events ADD CONSTRAINT record_audit_events_actor_kind_check
    CHECK(actor_kind IN ('administrator','user','service','anonymous'));
ALTER TABLE pgbase_meta.record_idempotency DROP CONSTRAINT record_idempotency_actor_kind_check;
ALTER TABLE pgbase_meta.record_idempotency ADD CONSTRAINT record_idempotency_actor_kind_check
    CHECK(actor_kind IN ('administrator','user','service','anonymous'));
