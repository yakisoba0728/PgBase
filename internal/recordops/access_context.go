package recordops

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func authorizeUserRecordOperation(ctx context.Context, tx pgx.Tx, catalog schema.Catalog, collection schema.Collection, principal Principal, recordID uuid.UUID, actions ...string) error {
	action := "view"
	if recordID == uuid.Nil {
		action = "create"
	}
	if len(actions) > 0 {
		action = actions[0]
	}
	return authorizeUserRecordOperationWithRequest(ctx, tx, catalog, collection, principal, recordID, action, nil)
}

func authorizeUserRecordOperationWithRequest(ctx context.Context, tx pgx.Tx, catalog schema.Catalog, collection schema.Collection, principal Principal, recordID uuid.UUID, action string, request *accessrule.RequestContext) error {
	if principal.Kind == PrincipalAdministrator {
		return nil
	}
	if err := recordaccess.AuthorizeSubject(ctx, tx, collection.AccessCollectionID(), principal, action); err != nil {
		return err
	}
	if recordID == uuid.Nil || principal.Kind == identity.Service {
		return nil
	}
	allowed, err := recordaccess.AllowsRecordInCatalog(ctx, tx, catalog, policyCollection(collection), principal, action, recordID, action == "create", request)
	if err != nil {
		return err
	}
	if !allowed {
		if action == "create" {
			return recordaccess.ErrDenied
		}
		return query.ErrRecordNotFound
	}
	return nil
}

func mutationRuleContext(collection schema.Collection, input map[string]json.RawMessage, before map[string]any, base *accessrule.RequestContext) accessrule.RequestContext {
	result := accessrule.RequestContext{Present: make(map[string]bool, len(input)), Changed: make(map[string]bool, len(input)), Body: make(map[string]json.RawMessage, len(input))}
	if base != nil {
		result.Method = base.Method
		result.Query = base.Query
		result.Headers = base.Headers
		result.Auth = base.Auth
		result.Context = base.Context
	}
	for name, raw := range input {
		result.Body[name] = append(json.RawMessage(nil), raw...)
		result.Present[name] = true
		if before == nil {
			result.Changed[name] = true
			continue
		}
		previous, exists := before[name]
		if !exists {
			result.Changed[name] = true
			continue
		}
		var field schema.Field
		for _, candidate := range collection.Fields {
			if candidate.Name == name {
				field = candidate
				break
			}
		}
		if field.Name == "" || !query.FieldInputEqualsStored(field, raw, previous) {
			result.Changed[name] = true
		}
	}
	return result
}
