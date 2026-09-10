alter table pgbase_meta.collections
    add column compatibility_name text,
    add column collection_type text not null default 'base'
        check (collection_type in ('base','auth','view')),
    add column system boolean not null default false,
    add column validation_mode text not null default 'postgres'
        check (validation_mode in ('postgres','pocketbase'));

update pgbase_meta.collections c
set collection_type = case when exists (
    select 1 from pgbase_meta.collection_views v where v.collection_id = c.id
) then 'view' else 'base' end;

create unique index collections_compatibility_name_ci_idx
    on pgbase_meta.collections (lower(compatibility_name))
    where compatibility_name is not null;

alter table pgbase_meta.fields
    add column system boolean not null default false;
