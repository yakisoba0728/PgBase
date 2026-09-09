alter table pgbase_meta.user_sessions
  add column session_kind text not null default 'user' check (session_kind in ('user','impersonation')),
  add column impersonating_admin_id uuid references pgbase_meta.admins(id) on delete cascade,
  add column impersonating_admin_session_id uuid references pgbase_meta.admin_sessions(id) on delete cascade,
  add constraint user_sessions_impersonation_origin check (
    (session_kind='user' and impersonating_admin_id is null and impersonating_admin_session_id is null) or
    (session_kind='impersonation' and impersonating_admin_id is not null and impersonating_admin_session_id is not null)
  );
create index user_sessions_impersonation_actor_idx on pgbase_meta.user_sessions(impersonating_admin_session_id) where session_kind='impersonation';
