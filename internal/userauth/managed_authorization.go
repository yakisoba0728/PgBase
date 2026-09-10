package userauth

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// authorizeManagedAuthRecord evaluates a collection's manage rule against the
// target record while exposing the distinct requesting identity as
// @request.auth. Empty manage rules never grant manager privileges.
func (s *Service) authorizeManagedAuthRecord(ctx context.Context, collectionID, actorID uuid.UUID, target AuthRecord, fields []schema.FieldInput, rule string, request map[string]any) bool {
	if actorID == uuid.Nil || target.ID == uuid.Nil || strings.TrimSpace(rule) == "" {
		return false
	}
	var actorEmail string
	err := s.pool.QueryRow(ctx, `select email from pgbase_meta.users where id=$1 and auth_collection_id=$2 and active`, actorID, collectionID).Scan(&actorEmail)
	if err != nil {
		return false
	}
	return EvaluateAuthRecordRule(rule, fields, AuthRuleContext{
		Principal: identity.Subject{Kind: identity.User, ID: actorID},
		AuthEmail: actorEmail,
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
