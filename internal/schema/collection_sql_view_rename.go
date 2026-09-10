package schema

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// PostgreSQL keeps a view bound to the renamed relation OID. Refreshing the
// stored source from pg_get_viewdef preserves that same binding in exports and
// verifies that catalog dependency metadata agrees with PostgreSQL.
func (s *Store) refreshSQLViewDefinitionsAfterRename(ctx context.Context, collectionID uuid.UUID, physicalName string) error {
	rows, err := s.db.Query(ctx, `with managed_views as (
		select views.collection_id,collections.table_name,views.dependencies,
			to_regclass(format('%I.%I','app',collections.table_name)) as view_oid,
			to_regclass(format('%I.%I','app',$2::text)) as source_oid
		from pgbase_meta.collection_views views
		join pgbase_meta.collections collections on collections.id=views.collection_id
		where views.sql is not null
	), dependencies as (
		select managed.*,
			exists(select 1 from pg_rewrite rewrite
				join pg_depend dependency on dependency.classid='pg_rewrite'::regclass and dependency.objid=rewrite.oid
				where rewrite.ev_class=managed.view_oid and dependency.refclassid='pg_class'::regclass
				  and dependency.refobjid=managed.source_oid) as physical_dependency
		from managed_views managed
	)
	select collection_id,pg_get_viewdef(view_oid,true),$1::uuid=any(dependencies),physical_dependency
	from dependencies where $1::uuid=any(dependencies) or physical_dependency`, collectionID, physicalName)
	if err != nil {
		return fmt.Errorf("inspect renamed SQL view dependencies: %w", err)
	}
	type update struct {
		id  uuid.UUID
		sql string
	}
	updates := []update{}
	for rows.Next() {
		var value update
		var metadataDependency, physicalDependency bool
		if err := rows.Scan(&value.id, &value.sql, &metadataDependency, &physicalDependency); err != nil {
			rows.Close()
			return err
		}
		if !metadataDependency || !physicalDependency {
			rows.Close()
			return ErrCatalogInvalid
		}
		updates = append(updates, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, value := range updates {
		if _, err := s.db.Exec(ctx, `update pgbase_meta.collection_views set sql=$2 where collection_id=$1`, value.id, value.sql); err != nil {
			return fmt.Errorf("refresh renamed SQL view definition: %w", err)
		}
	}
	return nil
}
