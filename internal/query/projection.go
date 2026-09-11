package query

import (
	"errors"

	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/responseprojection"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type Projection = responseprojection.Projection

func CompileProjection(requested string) (Projection, error) {
	projection, err := responseprojection.CompileProjection(requested)
	if err == nil {
		return projection, nil
	}
	var projectionError *responseprojection.Error
	if errors.As(err, &projectionError) {
		return Projection{}, validation("fields", string(projectionError.Code))
	}
	return Projection{}, err
}

type recordProjection struct{ Projection }

func compileRecordProjection(collection schema.Collection, principal Principal, requested string) (recordProjection, error) {
	projection, err := CompileProjection(requested)
	if err != nil {
		return recordProjection{}, err
	}
	available := make(map[string]schema.Field, len(collection.Fields))
	for _, field := range collection.Fields {
		available[field.Name] = field
	}
	for _, root := range projection.RootFields() {
		if root == "*" || root == "expand" || root == "collectionId" || root == "collectionName" || root == "created" || root == "updated" {
			continue
		}
		if _, system := recordProjectionSystemFields[root]; system {
			continue
		}
		field, found := available[root]
		if !found {
			return recordProjection{}, validation("fields", "unknown_field")
		}
		if field.Hidden && principal.Kind != identity.Administrator {
			return recordProjection{}, validation("fields", "hidden_field")
		}
	}
	return recordProjection{Projection: projection}, nil
}

var recordProjectionSystemFields = map[string]struct{}{"id": {}, "created_at": {}, "updated_at": {}}

func (p recordProjection) apply(record map[string]any) map[string]any {
	projected, _ := p.Apply(record).(map[string]any)
	return projected
}
func (p recordProjection) applyPage(page RecordPage) RecordPage {
	if p.Empty() {
		return page
	}
	for i := range page.Items {
		page.Items[i] = p.apply(page.Items[i])
	}
	return page
}
