package userauth

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// authorizeManagedAuthRecord evaluates a collection's manage rule against the
// target record while exposing the distinct requesting identity as
// @request.auth. Empty manage rules never grant manager privileges.
func (s *Service) authorizeManagedAuthRecord(ctx context.Context, collectionID, actorID uuid.UUID, target AuthRecord, fields []schema.FieldInput, rule string, request map[string]any) bool {
	if strings.TrimSpace(rule) == "" {
		return false
	}
	allowed := false
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		allowed, err = s.authorizeManagedAuthRecordTx(ctx, tx, collectionID, actorID, target, fields, rule, request)
		return err
	})
	return err == nil && allowed
}

func (s *Service) authorizeManagedAuthRecordTx(ctx context.Context, tx database.DBTX, collectionID, actorID uuid.UUID, target AuthRecord, fields []schema.FieldInput, rule string, request map[string]any) (bool, error) {
	if actorID == uuid.Nil || target.ID == uuid.Nil || strings.TrimSpace(rule) == "" {
		return false, nil
	}
	return EvaluateAuthRecordRuleInTx(ctx, tx, collectionID, rule, AuthRuleContext{
		Principal: identity.Subject{Kind: identity.User, ID: actorID},
		Current:   target,
		Request:   request,
	})
}

func authRecordMutationValues(in UpdateAuthRecordInput) map[string]any {
	values := make(map[string]any, len(in.Fields)+4)
	for name, value := range in.Fields {
		values[name] = value
	}
	if in.Email != nil {
		values["email"] = *in.Email
	}
	if in.Password != nil {
		// Rules may inspect presence/change but never receive the secret.
		values["password"] = "[redacted]"
	}
	if in.Verified != nil {
		values["verified"] = *in.Verified
	}
	if in.Active != nil {
		values["active"] = *in.Active
	}
	return values
}

func hasPrivilegedAuthMutation(in UpdateAuthRecordInput) bool {
	return in.Email != nil || in.Password != nil || in.Verified != nil || in.Active != nil
}
