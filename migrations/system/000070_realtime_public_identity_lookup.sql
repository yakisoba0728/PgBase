-- Replaying a deleted text ID resolves its most recent durable outbox receipt.
-- The outbox retains lifetime history, so the lookup must stay keyed by ID.
CREATE INDEX record_outbox_public_identity_idx
    ON pgbase_meta.record_outbox (collection_id, (envelope->'publicRecord'->>'id'), created_at DESC)
    WHERE (envelope->'publicRecord'->>'id') IS NOT NULL;
