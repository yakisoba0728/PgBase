package recordops

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type writePoliciesKey struct{}

type writeGrant struct {
	collection uuid.UUID
	principal  Principal
}

// writePolicies remembers, for one mutation attempt on one transaction, the
// admission grants and rule sets whose rows that transaction already locked.
//
// A user grant holds FOR SHARE on its collection_access and users rows, and an
// anonymous grant on its collection_access row, until the transaction ends, so
// no other transaction can change or remove them; the query ignores the
// action, so one grant answers every action on the collection. LoadRules holds
// FOR SHARE on a present collection_rules row. An absent row is remembered only
// after this attempt already holds the collection's grant: every writer that
// can insert the first rules row either takes collection_access FOR UPDATE
// (recordaccess.RuleService) or updates schema_state (schema imports), and both
// wait for this transaction's locks. The record kernel itself never writes
// those tables. Service grants are never remembered: their scope rows are not
// locked, so a Read Committed statement can observe a concurrent revocation.
//
// Entries are bound to the exact pgx.Tx that took the locks. A nested
// savepoint is a different value, so locks it might release on rollback are
// never remembered.
type writePolicies struct {
	tx     pgx.Tx
	grants map[writeGrant]struct{}
	rules  map[uuid.UUID]accessrule.Rules
}

func withWritePolicies(ctx context.Context, tx pgx.Tx, principal Principal) context.Context {
	if principal.Kind != identity.User && principal.Kind != identity.Anonymous {
		return ctx
	}
	return context.WithValue(ctx, writePoliciesKey{}, &writePolicies{
		tx:     tx,
		grants: make(map[writeGrant]struct{}),
		rules:  make(map[uuid.UUID]accessrule.Rules),
	})
}

func currentWritePolicies(ctx context.Context, tx pgx.Tx) *writePolicies {
	policies, _ := ctx.Value(writePoliciesKey{}).(*writePolicies)
	if policies == nil || policies.tx != tx {
		return nil
	}
	return policies
}

func authorizeWriteSubject(ctx context.Context, tx pgx.Tx, collectionID uuid.UUID, principal Principal, action string) error {
	policies := currentWritePolicies(ctx, tx)
	if policies == nil || (principal.Kind != identity.User && principal.Kind != identity.Anonymous) {
		return recordaccess.AuthorizeSubject(ctx, tx, collectionID, principal, action)
	}
	key := writeGrant{collection: collectionID, principal: principal}
	if _, ok := policies.grants[key]; ok {
		return nil
	}
	if err := recordaccess.AuthorizeSubject(ctx, tx, collectionID, principal, action); err != nil {
		return err
	}
	policies.grants[key] = struct{}{}
	return nil
}

func loadWriteRules(ctx context.Context, tx pgx.Tx, collectionID uuid.UUID, principal Principal) (accessrule.Rules, error) {
	policies := currentWritePolicies(ctx, tx)
	if policies == nil {
		rules, _, err := recordaccess.LoadRules(ctx, tx, collectionID)
		return rules, err
	}
	if rules, ok := policies.rules[collectionID]; ok {
		return rules, nil
	}
	rules, revision, err := recordaccess.LoadRules(ctx, tx, collectionID)
	if err != nil {
		return rules, err
	}
	// Revision zero is LoadRules' report of an absent, therefore unlocked, row.
	_, granted := policies.grants[writeGrant{collection: collectionID, principal: principal}]
	if revision != 0 || granted {
		policies.rules[collectionID] = rules
	}
	return rules, nil
}

func allowsWriteRecord(ctx context.Context, tx pgx.Tx, catalog schema.Catalog, collection schema.Collection, principal Principal, action string, id uuid.UUID, prospectiveOwner bool, request *accessrule.RequestContext) (bool, error) {
	rules, err := loadWriteRules(ctx, tx, collection.ID, principal)
	if err != nil {
		return false, err
	}
	return recordaccess.AllowsRecordInCatalogWithRules(ctx, tx, catalog, collection, rules, principal, action, id, prospectiveOwner, request)
}

func allowsWriteRecords(ctx context.Context, tx pgx.Tx, catalog schema.Catalog, collection schema.Collection, principal Principal, action string, ids []uuid.UUID, prospectiveOwner bool, request *accessrule.RequestContext) (bool, error) {
	if len(ids) == 0 {
		return true, nil
	}
	rules, err := loadWriteRules(ctx, tx, collection.ID, principal)
	if err != nil {
		return false, err
	}
	return recordaccess.AllowsRecordsInCatalogWithRules(ctx, tx, catalog, collection, rules, principal, action, ids, prospectiveOwner, request)
}
