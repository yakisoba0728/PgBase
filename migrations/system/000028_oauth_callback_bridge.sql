alter table pgbase_meta.user_oauth_states
  add column provider_redirect_uri text,
  add column callback_received_at timestamptz;

update pgbase_meta.user_oauth_states set provider_redirect_uri = redirect_uri;
alter table pgbase_meta.user_oauth_states alter column provider_redirect_uri set not null;
