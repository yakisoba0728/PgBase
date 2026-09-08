ALTER TABLE pgbase_meta.record_outbox ADD COLUMN owner_user_id uuid, ADD COLUMN record_snapshot jsonb;
-- Existing events have no reliable historic audience and are never exposed.
CREATE TABLE pgbase_meta.outbox_delivery (
    event_id uuid PRIMARY KEY REFERENCES pgbase_meta.record_outbox(event_id) ON DELETE CASCADE,
    lease_token uuid,
    lease_until timestamptz,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    delivered_at timestamptz,
    dead_at timestamptz
);
CREATE INDEX outbox_delivery_pending_idx ON pgbase_meta.outbox_delivery(available_at, event_id)
    WHERE delivered_at IS NULL AND dead_at IS NULL;
CREATE TABLE pgbase_meta.realtime_events (
    sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_id uuid NOT NULL UNIQUE REFERENCES pgbase_meta.record_outbox(event_id) ON DELETE RESTRICT,
    owner_user_id uuid,
    collection_id uuid NOT NULL,
    collection_name text NOT NULL,
    record_id uuid NOT NULL,
    operation text NOT NULL CHECK (operation IN ('create', 'update', 'delete')),
    record_version bigint NOT NULL,
    delivered_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX realtime_events_audience_idx ON pgbase_meta.realtime_events(owner_user_id, collection_id, sequence);
CREATE TABLE pgbase_meta.realtime_retention (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    floor_sequence bigint NOT NULL DEFAULT 0
);
INSERT INTO pgbase_meta.realtime_retention DEFAULT VALUES;
