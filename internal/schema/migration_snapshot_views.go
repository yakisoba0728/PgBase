package schema

import "fmt"

func applyMigrationViewFields(plan *Plan, snapshot MigrationSnapshotCollection) error {
	if len(snapshot.Fields) != len(plan.Definition.Fields) {
		return validation("collections."+snapshot.Name+".fields", "view_field_mismatch")
	}
	for index := range plan.Definition.Fields {
		provided, inferred := snapshot.Fields[index], &plan.Definition.Fields[index]
		if provided.Name != inferred.Name || provided.Type != inferred.Type || provided.Required != inferred.Required ||
			provided.Unique != inferred.Unique || provided.Indexed != inferred.Indexed || len(provided.Default) != 0 || provided.Relation != nil {
			return validation("collections."+snapshot.Name+".fields", "view_field_mismatch")
		}
		if err := validateFieldInputMetadata(fmt.Sprintf("collections.%s.fields.%d", snapshot.Name, index), &provided); err != nil {
			return err
		}
		inferred.Options = FieldOptions{}
		if provided.Options != nil {
			inferred.Options = cloneFieldOptions(*provided.Options)
		}
		inferred.Hidden, inferred.Presentable, inferred.Help = provided.Hidden, provided.Presentable, provided.Help
		if provided.System != nil {
			inferred.System = *provided.System
		}
	}
	return nil
}

func orderedMigrationViews(snapshot MigrationSnapshot, reverse bool) []MigrationSnapshotCollection {
	views := make([]SchemaDocumentCollection, 0)
	byName := map[string]MigrationSnapshotCollection{}
	for _, item := range snapshot.Collections {
		if item.View == nil {
			continue
		}
		views = append(views, SchemaDocumentCollection{Name: item.Name, DependsOn: append([]string(nil), item.DependsOn...)})
		byName[item.Name] = item
	}
	ordered, err := orderImportedViews(views)
	if err != nil {
		return nil
	}
	result := make([]MigrationSnapshotCollection, 0, len(ordered))
	for _, item := range ordered {
		result = append(result, byName[item.Name])
	}
	if reverse {
		for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
			result[left], result[right] = result[right], result[left]
		}
	}
	return result
}

func countMigrationViews(snapshot MigrationSnapshot) int {
	count := 0
	for _, item := range snapshot.Collections {
		if item.View != nil {
			count++
		}
	}
	return count
}
