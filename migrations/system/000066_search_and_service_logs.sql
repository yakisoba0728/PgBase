CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA pgbase_meta;
ALTER TABLE pgbase_meta.request_logs DROP CONSTRAINT request_logs_auth_kind_check;
ALTER TABLE pgbase_meta.request_logs ADD CONSTRAINT request_logs_auth_kind_check CHECK (auth_kind IN ('admin','user','service'));
ALTER TABLE pgbase_meta.request_logs ADD COLUMN search_text text GENERATED ALWAYS AS
 (lower(route || ' ' || coalesce(host(client_ip),'') || ' ' || coalesce(auth_kind,'') || ' ' || coalesce(auth_id::text,''))) STORED;
DO $migration$
DECLARE extension_schema name;
BEGIN
 SELECT n.nspname INTO STRICT extension_schema FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='pg_trgm';
 EXECUTE format('CREATE INDEX request_logs_search_idx ON pgbase_meta.request_logs USING gin (search_text %I.gin_trgm_ops)',extension_schema);
 EXECUTE format('CREATE INDEX users_email_search_idx ON pgbase_meta.users USING gin (email %I.gin_trgm_ops)',extension_schema);
END
$migration$;
CREATE INDEX outbox_delivery_dead_idx ON pgbase_meta.outbox_delivery(dead_at DESC,event_id DESC)
 WHERE dead_at IS NOT NULL AND delivered_at IS NULL;
