package schema

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5"
)

func deleteMissingChanges(catalog Catalog, keep map[string]struct{}) ([]Change, error) {
	projected := cloneCatalog(catalog)
	remaining := map[string]bool{}
	for name := range catalog.Collections {
		if _, ok := keep[name]; !ok {
			remaining[name] = true
		}
	}
	changes := []Change{}
	for len(remaining) > 0 {
		progress := false
		names := make([]string, 0, len(remaining))
		for name := range remaining {
			names = append(names, name)
		}
		sort.SliceStable(names, func(i, j int) bool {
			left, right := projected.Collections[names[i]], projected.Collections[names[j]]
			if left.IsView() != right.IsView() {
				return left.IsView()
			}
			return names[i] < names[j]
		})
		for _, name := range names {
			change := NewDeleteChange(name)
			plan, err := compilePlan(projected, change, false)
			if err != nil {
				continue
			}
			changes = append(changes, change)
			projected, err = projectCatalog(projected, change, plan)
			if err != nil {
				return nil, err
			}
			delete(remaining, name)
			progress = true
		}
		if !progress {
			return nil, validation("deleteMissing", "collection_referenced")
		}
	}
	return changes, nil
}

func validateDeleteMissingProtection(ctx context.Context, tx pgx.Tx, catalog Catalog, document SchemaDocument) error {
	provided := map[string]bool{}
	for _, item := range document.Collections {
		provided[item.Name] = true
	}
	for name, collection := range catalog.Collections {
		if !provided[name] && collection.System {
			return validation("deleteMissing."+name, "system_collection_protected")
		}
	}
	providedRealms := map[string]bool{}
	for _, realm := range document.AuthCollections {
		providedRealms[realm.Name] = true
	}
	rows, err := tx.Query(ctx, `select name from pgbase_meta.auth_collections order by name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if !providedRealms[name] {
			return validation("deleteMissing.authCollections."+name, "auth_realm_protected")
		}
	}
	return rows.Err()
}
