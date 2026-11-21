package recordaccess

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// AllowsRecord evaluates the action on the row inside the caller's pinned
// transaction. It also works on newly inserted rows before ownership publish.
func AllowsRecord(ctx context.Context, tx pgx.Tx, collection schema.Collection, principal identity.Subject, action string, id uuid.UUID, prospectiveOwner bool) (bool, error) {
	return AllowsRecordInCatalog(ctx, tx, schema.Catalog{Collections: map[string]schema.Collection{collection.Name: collection}}, collection, principal, action, id, prospectiveOwner, nil)
}

func AllowsRecordWithRequest(ctx context.Context, tx pgx.Tx, collection schema.Collection, principal identity.Subject, action string, id uuid.UUID, prospectiveOwner bool, request *accessrule.RequestContext) (bool, error) {
	return AllowsRecordInCatalog(ctx, tx, schema.Catalog{Collections: map[string]schema.Collection{collection.Name: collection}}, collection, principal, action, id, prospectiveOwner, request)
}

func AllowsRecordInCatalog(ctx context.Context, tx pgx.Tx, catalog schema.Catalog, collection schema.Collection, principal identity.Subject, action string, id uuid.UUID, prospectiveOwner bool, request *accessrule.RequestContext) (bool, error) {
	rules, _, err := LoadRules(ctx, tx, collection.ID)
	if err != nil {
		return false, err
	}
	return AllowsRecordInCatalogWithRules(ctx, tx, catalog, collection, rules, principal, action, id, prospectiveOwner, request)
}

// AllowsRecordInCatalogWithRules evaluates rules the caller already loaded
// with LoadRules in this transaction. The caller owns the claim that the
// loaded rules are still the ones LoadRules would return now.
func AllowsRecordInCatalogWithRules(ctx context.Context, tx pgx.Tx, catalog schema.Catalog, collection schema.Collection, rules accessrule.Rules, principal identity.Subject, action string, id uuid.UUID, prospectiveOwner bool, request *accessrule.RequestContext) (bool, error) {
	rule, err := rules.Action(action)
	if err != nil {
		return false, err
	}
	predicate, args, err := compileRecordRule(catalog, collection, rule, principal, ruleCompileOptions{
		request: request, argOffset: 1, prospectiveOwner: prospectiveOwner,
	})
	if err != nil {
		return false, err
	}
	args = append([]any{id}, args...)
	var allowed bool
	err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+pgx.Identifier{"app", collection.TableName}.Sanitize()+" WHERE id=$1 AND ("+predicate+"))", args...).Scan(&allowed)
	return allowed, err
}

// AllowsRecords preserves set-based mutation work for large cascade batches.
func AllowsRecords(ctx context.Context, tx pgx.Tx, collection schema.Collection, principal identity.Subject, action string, ids []uuid.UUID, prospectiveOwner bool) (bool, error) {
	return AllowsRecordsInCatalog(ctx, tx, schema.Catalog{Collections: map[string]schema.Collection{collection.Name: collection}}, collection, principal, action, ids, prospectiveOwner)
}

func AllowsRecordsInCatalog(ctx context.Context, tx pgx.Tx, catalog schema.Catalog, collection schema.Collection, principal identity.Subject, action string, ids []uuid.UUID, prospectiveOwner bool) (bool, error) {
	return AllowsRecordsInCatalogWithRequest(ctx, tx, catalog, collection, principal, action, ids, prospectiveOwner, nil)
}

func AllowsRecordsInCatalogWithRequest(ctx context.Context, tx pgx.Tx, catalog schema.Catalog, collection schema.Collection, principal identity.Subject, action string, ids []uuid.UUID, prospectiveOwner bool, request *accessrule.RequestContext) (bool, error) {
	if len(ids) == 0 {
		return true, nil
	}
	rules, _, err := LoadRules(ctx, tx, collection.ID)
	if err != nil {
		return false, err
	}
	return AllowsRecordsInCatalogWithRules(ctx, tx, catalog, collection, rules, principal, action, ids, prospectiveOwner, request)
}

// AllowsRecordsInCatalogWithRules is the set-based form of
// AllowsRecordInCatalogWithRules.
func AllowsRecordsInCatalogWithRules(ctx context.Context, tx pgx.Tx, catalog schema.Catalog, collection schema.Collection, rules accessrule.Rules, principal identity.Subject, action string, ids []uuid.UUID, prospectiveOwner bool, request *accessrule.RequestContext) (bool, error) {
	if len(ids) == 0 {
		return true, nil
	}
	rule, err := rules.Action(action)
	if err != nil {
		return false, err
	}
	predicate, args, err := compileRecordRule(catalog, collection, rule, principal, ruleCompileOptions{
		request: request, argOffset: 1, prospectiveOwner: prospectiveOwner,
	})
	if err != nil {
		return false, err
	}
	args = append([]any{ids}, args...)
	var count int
	err = tx.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{"app", collection.TableName}.Sanitize()+" WHERE id=ANY($1::uuid[]) AND ("+predicate+")", args...).Scan(&count)
	return count == len(ids), err
}
