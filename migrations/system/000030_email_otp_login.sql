alter table pgbase_meta.auth_collection_policies
  add column otp_enabled boolean not null default false,
  add column otp_duration_seconds integer not null default 300 check (otp_duration_seconds between 60 and 900),
  add column otp_max_attempts integer not null default 5 check (otp_max_attempts between 1 and 10);

alter table pgbase_meta.auth_mail_templates drop constraint auth_mail_templates_purpose_check;
alter table pgbase_meta.auth_mail_templates add constraint auth_mail_templates_purpose_check
  check (purpose in ('verify','password_reset','email_change','otp'));

create table pgbase_meta.user_email_otps (
  id uuid primary key,
  auth_collection_id uuid not null references pgbase_meta.auth_collections(id) on delete cascade,
  user_id uuid not null references pgbase_meta.users(id) on delete cascade,
  code_hash bytea not null check (octet_length(code_hash)=32),
  attempts integer not null default 0,
  max_attempts integer not null check (max_attempts between 1 and 10),
  expires_at timestamptz not null,
  consumed_at timestamptz,
  created_at timestamptz not null default current_timestamp
);
create index user_email_otps_active_idx on pgbase_meta.user_email_otps(id,expires_at) where consumed_at is null;
create unique index user_email_otps_one_active_user on pgbase_meta.user_email_otps(user_id) where consumed_at is null;
