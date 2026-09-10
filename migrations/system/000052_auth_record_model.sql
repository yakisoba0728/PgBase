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

alter table pgbase_meta.users add constraint users_auth_collection_id_id_key unique(auth_collection_id,id);
alter table pgbase_meta.record_files drop constraint record_files_collection_id_record_id_fkey;
alter table pgbase_meta.record_files alter column collection_id drop not null;
alter table pgbase_meta.record_files alter column record_id drop not null;
alter table pgbase_meta.record_files
  add column auth_collection_id uuid references pgbase_meta.auth_collections(id) on delete cascade,
  add column auth_user_id uuid,
  add constraint record_files_auth_user_fk foreign key(auth_collection_id,auth_user_id)
    references pgbase_meta.users(auth_collection_id,id) on delete cascade,
  add constraint record_files_owner_kind_check check (
    (collection_id is not null and record_id is not null and auth_collection_id is null and auth_user_id is null)
    or (collection_id is null and record_id is null and auth_collection_id is not null and auth_user_id is not null)
  ),
  add constraint record_files_base_record_fk foreign key(collection_id,record_id)
    references pgbase_meta.record_revisions(collection_id,record_id) on delete cascade;
create index record_files_auth_record_idx on pgbase_meta.record_files(auth_collection_id,auth_user_id,field_name,created_at,id) where status='ready';
