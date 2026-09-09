create table pgbase_meta.schema_import_operations (
  operation_id uuid primary key,
  administrator_id uuid not null references pgbase_meta.admins(id) on delete cascade,
  idempotency_hash bytea not null check (octet_length(idempotency_hash)=32),
  request_hash bytea not null check (octet_length(request_hash)=32),
  base_version bigint not null,
  status text not null check (status in ('running','committed','failed')),
  committed_version bigint,
  applied integer not null default 0,
  failure_code text,
  created_at timestamptz not null default clock_timestamp(),
  updated_at timestamptz not null default clock_timestamp(),
  completed_at timestamptz,
  unique (administrator_id,idempotency_hash)
);
