alter table pgbase_meta.auth_collections
  add column fields_json jsonb not null default '[]'::jsonb check (jsonb_typeof(fields_json)='array'),
  add column list_rule text not null default '',
  add column view_rule text not null default '@request.auth.id = id',
  add column create_rule text not null default '',
  add column update_rule text not null default '@request.auth.id = id',
  add column delete_rule text not null default '@request.auth.id = id',
  add column auth_rule text not null default '',
  add column manage_rule text not null default '@request.auth.id = id';

update pgbase_meta.auth_collections c set fields_json=(
  select coalesce(jsonb_agg(jsonb_build_object('name',e.key,'type',e.value,'required',false,'unique',false,'indexed',false) order by e.key),'[]'::jsonb)
  from jsonb_each_text(c.profile_schema) e
);

create table pgbase_meta.user_auth_origins (
  id uuid primary key,
  user_id uuid not null references pgbase_meta.users(id) on delete cascade,
  fingerprint_hash bytea not null check (octet_length(fingerprint_hash)=32),
  first_seen_at timestamptz not null,
  last_seen_at timestamptz not null,
  unique(user_id,fingerprint_hash)
);


alter table pgbase_meta.user_oauth_identities add column id uuid not null default gen_random_uuid();
alter table pgbase_meta.user_oauth_identities add constraint user_oauth_identities_id_key unique(id);
alter table pgbase_meta.user_totp_factors add column id uuid not null default gen_random_uuid();
alter table pgbase_meta.user_totp_factors add constraint user_totp_factors_id_key unique(id);
