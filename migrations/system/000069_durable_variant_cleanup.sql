-- An interrupted object write may complete remotely after its PostgreSQL
-- sessions disappear. Keep its deletion intent until the writer acknowledges
-- completion or publishes a live reference; a successful Delete alone cannot
-- prove that no late write will follow it.
ALTER TABLE pgbase_meta.file_object_deletions
    ADD COLUMN retain_on_success boolean NOT NULL DEFAULT false,
    ADD COLUMN attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    ADD COLUMN last_attempt_at timestamptz,
    ADD COLUMN next_attempt_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ADD COLUMN last_error text NOT NULL DEFAULT '';

CREATE INDEX file_object_deletions_due
    ON pgbase_meta.file_object_deletions(next_attempt_at, queued_at, object_key);

-- Older cleanup workers must not silently retire an uncertainty tombstone.
-- Current workers schedule another pass instead of issuing this DELETE.
CREATE FUNCTION pgbase_meta.protect_uncertain_object_intent()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.retain_on_success AND NOT EXISTS (
        SELECT 1 FROM pgbase_meta.record_files
        WHERE status IN ('pending', 'ready')
          AND (object_key = OLD.object_key OR thumbnail_object_key = OLD.object_key)
        UNION ALL
        SELECT 1 FROM pgbase_meta.record_file_variants WHERE object_key = OLD.object_key
        UNION ALL
        SELECT 1 FROM pgbase_meta.extension_objects WHERE object_key = OLD.object_key
    ) THEN
        RAISE EXCEPTION 'uncertain object write requires retained cleanup intent'
            USING ERRCODE = '55000';
    END IF;
    RETURN OLD;
END;
$$;

CREATE TRIGGER file_object_deletions_protect_uncertain
    BEFORE DELETE ON pgbase_meta.file_object_deletions
    FOR EACH ROW EXECUTE FUNCTION pgbase_meta.protect_uncertain_object_intent();
