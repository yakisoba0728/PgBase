ALTER TABLE pgbase_meta.request_logs
  ADD COLUMN client_ip inet,
  ADD COLUMN auth_kind text CHECK (auth_kind IN ('admin','user')),
  ADD COLUMN auth_id uuid,
  ADD CONSTRAINT request_logs_auth_context CHECK ((auth_kind IS NULL) = (auth_id IS NULL));
