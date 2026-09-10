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
	rules, _, err := LoadRules(ctx, tx, collection.ID)
	if err != nil {
		return false, err
	}
	rule, err := rules.Action(action)
	if err != nil {
		return false, err
	}
	predicate, args, err := accessrule.Compile(rule, collection, collection.TableName, principal, 1, prospectiveOwner)
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
	if len(ids) == 0 {
		return true, nil
	}
	rules, _, err := LoadRules(ctx, tx, collection.ID)
	if err != nil {
		return false, err
	}
	rule, err := rules.Action(action)
	if err != nil {
		return false, err
	}
	predicate, args, err := accessrule.Compile(rule, collection, collection.TableName, principal, 1, prospectiveOwner)
	if err != nil {
		return false, err
	}
	args = append([]any{ids}, args...)
	var count int
	err = tx.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{"app", collection.TableName}.Sanitize()+" WHERE id=ANY($1::uuid[]) AND ("+predicate+")", args...).Scan(&count)
	return count == len(ids), err
}
