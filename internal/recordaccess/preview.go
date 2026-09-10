package recordaccess

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
)

// RulePreview evaluates an unsaved rule against existing rows without modifying
// either data or policy. Omitting UserID evaluates the anonymous principal.
type RulePreview struct {
	Rule      accessrule.Rule `json:"rule"`
	Action    string          `json:"action"`
	UserID    uuid.UUID       `json:"userId"`
	RecordIDs []uuid.UUID     `json:"recordIds"`
}

type RuleDecision struct {
	RecordID uuid.UUID `json:"recordId"`
	Allowed  bool      `json:"allowed"`
}

func (s *RuleService) Preview(ctx context.Context, name string, input RulePreview) ([]RuleDecision, error) {
	if len(input.RecordIDs) == 0 || len(input.RecordIDs) > 50 {
		return nil, accessrule.ErrInvalid
	}
	switch input.Action {
	case "list", "view", "update", "delete":
	default:
		return nil, accessrule.ErrInvalid
	}
	seen := make(map[uuid.UUID]bool, len(input.RecordIDs))
	results := make([]RuleDecision, len(input.RecordIDs))
	for i, id := range input.RecordIDs {
		if id == uuid.Nil || seen[id] {
			return nil, accessrule.ErrInvalid
		}
		seen[id] = true
		results[i].RecordID = id
	}
	subject := identity.Subject{Kind: identity.Anonymous}
	if input.UserID != uuid.Nil {
		subject = identity.Subject{Kind: identity.User, ID: input.UserID}
	}
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		catalog, err := s.schemas.EnsureCurrent(ctx, tx)
		if err != nil {
			return err
		}
		collection, ok := catalog.Collections[name]
		if !ok {
			return ErrNotFound
		}
		predicate, args, err := accessrule.Compile(input.Rule, collection, collection.TableName, subject, 1, false)
		if err != nil {
			return err
		}
		if err := AuthorizeSubject(ctx, tx, collection.ID, subject, input.Action); err != nil {
			if errors.Is(err, ErrDenied) {
				return nil
			}
			return err
		}
		args = append([]any{input.RecordIDs}, args...)
		rows, err := tx.Query(ctx, "SELECT id FROM "+pgx.Identifier{"app", collection.TableName}.Sanitize()+" WHERE id=ANY($1::uuid[]) AND ("+predicate+")", args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		allowed := make(map[uuid.UUID]bool)
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			allowed[id] = true
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for i := range results {
			results[i].Allowed = allowed[results[i].RecordID]
		}
		return nil
	})
	return results, err
}
