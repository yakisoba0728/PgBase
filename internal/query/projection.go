package query

import (
	"strings"

	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type recordProjection struct {
	fields map[string]struct{}
}

func compileRecordProjection(collection schema.Collection, principal Principal, requested string) (recordProjection, error) {
	if requested == "" {
		return recordProjection{}, nil
	}
	available := make(map[string]schema.Field, len(collection.Fields))
	for _, field := range collection.Fields {
		available[field.Name] = field
	}
	projection := recordProjection{fields: make(map[string]struct{})}
	for _, name := range strings.Split(requested, ",") {
		if name == "" || strings.ContainsAny(name, ".*") {
			return recordProjection{}, validation("fields", "invalid_field")
		}
		if _, duplicate := projection.fields[name]; duplicate {
			return recordProjection{}, validation("fields", "duplicate_field")
		}
		if _, system := recordProjectionSystemFields[name]; !system {
			field, found := available[name]
			if !found {
				return recordProjection{}, validation("fields", "unknown_field")
			}
			if field.Hidden && principal.Kind != identity.Administrator {
				return recordProjection{}, validation("fields", "hidden_field")
			}
		}
		projection.fields[name] = struct{}{}
	}
	return projection, nil
}

var recordProjectionSystemFields = map[string]struct{}{
	"id": {}, "created_at": {}, "updated_at": {},
}

func (p recordProjection) apply(record map[string]any) map[string]any {
	if len(p.fields) == 0 {
		return record
	}
	projected := make(map[string]any, len(p.fields))
	for name := range p.fields {
		if value, found := record[name]; found {
			projected[name] = value
		}
	}
	return projected
}

func (p recordProjection) applyPage(page RecordPage) RecordPage {
	if len(p.fields) == 0 {
		return page
	}
	for index := range page.Items {
		page.Items[index] = p.apply(page.Items[index])
	}
	return page
}
