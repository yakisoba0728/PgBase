alter table pgbase_meta.auth_collections
  alter column list_rule drop not null,
  alter column view_rule drop not null,
  alter column create_rule drop not null,
  alter column update_rule drop not null,
  alter column delete_rule drop not null,
  alter column auth_rule drop not null,
  alter column manage_rule drop not null;
