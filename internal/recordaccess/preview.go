package recordaccess

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
)

// RulePreview evaluates an unsaved rule against existing rows without modifying
// either data or policy. Omitting UserID evaluates the anonymous principal.
type RulePreview struct {
	Rule      accessrule.Rule            `json:"rule"`
	Action    string                     `json:"action"`
	UserID    uuid.UUID                  `json:"userId"`
	RecordIDs []uuid.UUID                `json:"recordIds"`
	Request   *accessrule.RequestContext `json:"request,omitempty"`
}

type RuleDecision struct {
	RecordID uuid.UUID `json:"recordId"`
	Allowed  bool      `json:"allowed"`
}

func (s *RuleService) Preview(ctx context.Context, name string, input RulePreview) ([]RuleDecision, error) {
	if len(input.RecordIDs) == 0 || len(input.RecordIDs) > 50 || !validRulePreviewRequest(input.Request) {
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
		predicate, args, err := compileRecordRule(catalog, collection, input.Rule, subject, ruleCompileOptions{
			request: input.Request, argOffset: 1, requestFallback: input.Action == "update",
		})
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

func validRulePreviewRequest(request *accessrule.RequestContext) bool {
	if request == nil {
		return true
	}
	if len(request.Present) > 64 || len(request.Changed) > 64 || len(request.Body) > 64 || len(request.Query) > 64 || len(request.Headers) > 64 || len(request.Auth) > 64 || len(request.Context) > 64 {
		return false
	}
	raw, err := json.Marshal(request)
	return err == nil && len(raw) <= 64<<10
}
