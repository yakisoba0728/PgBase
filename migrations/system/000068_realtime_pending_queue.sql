-- Queue admission belongs to the outbox transaction, including transactions
-- written by an older binary. A polling worker must not rediscover work by
-- anti-joining the complete retained mutation history on every pass.
ALTER TABLE pgbase_meta.outbox_delivery
    ADD COLUMN collection_id uuid,
    ADD COLUMN record_id uuid,
    ADD COLUMN record_version bigint;

-- Completed historical markers need no ordering metadata. Updating only the
-- pending/dead subset avoids rewriting lifetime delivered history on upgrade.
UPDATE pgbase_meta.outbox_delivery delivery
SET collection_id = outbox.collection_id,
    record_id = outbox.record_id,
    record_version = outbox.record_version
FROM pgbase_meta.record_outbox outbox
WHERE delivery.event_id = outbox.event_id AND delivery.delivered_at IS NULL;

ALTER TABLE pgbase_meta.outbox_delivery
    ADD CONSTRAINT outbox_delivery_pending_identity CHECK (
        delivered_at IS NOT NULL OR (
            collection_id IS NOT NULL AND record_id IS NOT NULL
            AND record_version IS NOT NULL AND record_version > 0
        )
    );

-- Older workers and supported diagnostics may insert only event_id. Resolve
-- their immutable ordering identity from the source row before constraints.
CREATE FUNCTION pgbase_meta.populate_outbox_delivery_identity()
RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF NEW.collection_id IS NULL OR NEW.record_id IS NULL OR NEW.record_version IS NULL THEN
        SELECT collection_id, record_id, record_version
        INTO NEW.collection_id, NEW.record_id, NEW.record_version
        FROM pgbase_meta.record_outbox WHERE event_id = NEW.event_id;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'delivery event does not exist' USING ERRCODE = '23503';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER outbox_delivery_populate_identity
BEFORE INSERT ON pgbase_meta.outbox_delivery
FOR EACH ROW EXECUTE FUNCTION pgbase_meta.populate_outbox_delivery_identity();

-- A statement trigger keeps bulk mutation admission set-based. PostgreSQL holds
-- the outbox table lock through migration commit, so no concurrent old writer
-- can commit an event between this trigger's installation and the backfill.
CREATE FUNCTION pgbase_meta.enqueue_record_outbox_delivery()
RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    INSERT INTO pgbase_meta.outbox_delivery(event_id, collection_id, record_id, record_version)
    SELECT event_id, collection_id, record_id, record_version FROM new_outbox_events
    ON CONFLICT (event_id) DO NOTHING;
    RETURN NULL;
END;
$$;

CREATE TRIGGER record_outbox_enqueue_delivery
AFTER INSERT ON pgbase_meta.record_outbox
REFERENCING NEW TABLE AS new_outbox_events
FOR EACH STATEMENT EXECUTE FUNCTION pgbase_meta.enqueue_record_outbox_delivery();

INSERT INTO pgbase_meta.outbox_delivery(event_id, collection_id, record_id, record_version)
SELECT outbox.event_id, outbox.collection_id, outbox.record_id, outbox.record_version
FROM pgbase_meta.record_outbox outbox
WHERE NOT EXISTS (SELECT 1 FROM pgbase_meta.outbox_delivery delivery WHERE delivery.event_id = outbox.event_id)
ON CONFLICT (event_id) DO NOTHING;

-- Dead predecessors intentionally remain in this index and block later
-- versions until an operator retries them. Delivered history is absent.
CREATE INDEX outbox_delivery_predecessor_idx
ON pgbase_meta.outbox_delivery(collection_id, record_id, record_version)
WHERE delivered_at IS NULL;
