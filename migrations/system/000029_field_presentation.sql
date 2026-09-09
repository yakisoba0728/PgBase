alter table pgbase_meta.fields
    add column hidden boolean not null default false,
    add column presentable boolean not null default false,
    add column help text not null default '',
    add constraint fields_help_length check (char_length(help) <= 300),
    add constraint fields_hidden_presentable check (not (hidden and presentable));
