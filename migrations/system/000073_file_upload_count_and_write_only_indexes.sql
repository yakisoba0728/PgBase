-- The upload capacity check counts pending and ready rows of one record field.
-- Ready rows already use record_files_record_idx and record_files_auth_record_idx;
-- these cover the short-lived pending rows, so the check never scans the table.
CREATE INDEX record_files_pending_record_idx
    ON pgbase_meta.record_files (collection_id, record_id, field_name)
    WHERE status = 'pending';
CREATE INDEX record_files_pending_auth_record_idx
    ON pgbase_meta.record_files (auth_collection_id, auth_user_id, field_name)
    WHERE status = 'pending';

-- No PgBase query reads these: audit events are found by event_id (primary key)
-- and outbox rows by event_id or record_outbox_public_identity_idx. Every
-- record mutation still paid to maintain them. Lookups by record remain covered
-- by record_audit_events_resource_version_unique.
DROP INDEX pgbase_meta.record_audit_events_resource_idx;
DROP INDEX pgbase_meta.record_audit_events_actor_idx;
DROP INDEX pgbase_meta.record_outbox_created_idx;
