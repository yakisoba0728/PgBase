package schema

import "reflect"

func sameDeclarativeView(current *ViewSpec, imported *ViewInput) bool {
	return current.SQL == "" && current.Source == imported.Source && reflect.DeepEqual(current.Projection, imported.Projection) && reflect.DeepEqual(current.Filter, imported.Filter)
}

func sameViewPresentation(current Collection, imported []FieldInput) bool {
	if len(current.Fields) != len(imported) {
		return false
	}
	for i, field := range current.Fields {
		provided := imported[i]
		if provided.ID != nil || field.Name != provided.Name || field.Type != provided.Type || field.Required != provided.Required || field.Unique != provided.Unique || field.Indexed != provided.Indexed || string(field.Default) != string(provided.Default) || !reflect.DeepEqual(cloneFieldOptionsPointer(field.Options), provided.Options) || (field.Relation != nil) != (provided.Relation != nil) || field.Hidden != provided.Hidden || field.Presentable != provided.Presentable || field.Help != provided.Help {
			return false
		}
	}
	return true
}

func applyImportedViewPresentation(plan *Plan, item SchemaDocumentCollection) error {
	if item.View == nil || len(item.Fields) == 0 {
		return nil
	}
	if len(item.Fields) != len(plan.Definition.Fields) {
		return validation("collections."+item.Name+".fields", "view_field_mismatch")
	}
	for i := range plan.Definition.Fields {
		provided, inferred := item.Fields[i], &plan.Definition.Fields[i]
		if provided.ID != nil || provided.Name != inferred.Name || provided.Type != inferred.Type || provided.Required != inferred.Required || provided.Unique != inferred.Unique || provided.Indexed != inferred.Indexed || string(provided.Default) != string(inferred.Default) || !reflect.DeepEqual(provided.Options, cloneFieldOptionsPointer(inferred.Options)) || (provided.Relation != nil) != (inferred.Relation != nil) {
			return validation("collections."+item.Name+".fields", "view_field_mismatch")
		}
		inferred.Hidden, inferred.Presentable, inferred.Help = provided.Hidden, provided.Presentable, provided.Help
	}
	return nil
}

func omitUnchangedDeclarativeViewDDL(catalog Catalog, change Change, item SchemaDocumentCollection, plan *Plan, companions bool) error {
	if change.kind != changeUpdate || item.View == nil || item.View.SQL != "" {
		return nil
	}
	current, ok := collectionForUpdate(catalog, change.update)
	if !ok || current.View == nil || current.IsSQLView() || !sameDeclarativeView(current.View, item.View) {
		return nil
	}
	if len(current.Fields) != len(plan.Definition.Fields) {
		return nil
	}
	for i, field := range current.Fields {
		planned := plan.Definition.Fields[i]
		if field.Name != planned.Name || field.ColumnName != planned.ColumnName || field.Type != planned.Type {
			return nil
		}
	}
	plan.Statements = []string{}
	compilerVersion := schemaPlanCompilerV1
	if companions {
		compilerVersion = schemaPlanCompilerV2
	}
	checksum, err := planChecksumWithCompilerVersion(*plan, compilerVersion)
	if err == nil {
		plan.Checksum = checksum
	}
	return err
}
