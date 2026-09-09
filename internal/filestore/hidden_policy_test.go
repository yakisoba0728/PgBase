package filestore

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/identity"
)

func TestHiddenFieldPolicyGuardsEveryFileOperation(t *testing.T) {
	s := &Service{
		authorize: func(context.Context, pgx.Tx, identity.Subject, uuid.UUID, uuid.UUID, string, Operation) error {
			return nil
		},
		options: Options{MaximumBytes: 1, Policy: func(context.Context, pgx.Tx, uuid.UUID, string) (FieldPolicy, error) {
			return FieldPolicy{Hidden: true}, nil
		}},
	}
	user := identity.Subject{Kind: identity.User, ID: uuid.New()}
	for _, operation := range []Operation{OperationRead, OperationWrite} {
		if err := s.authorizeField(context.Background(), nil, user, uuid.New(), uuid.New(), "secret", operation); err != ErrNotFound {
			t.Fatalf("%v err=%v", operation, err)
		}
	}
	admin := identity.Subject{Kind: identity.Administrator, ID: uuid.New()}
	if err := s.authorizeField(context.Background(), nil, admin, uuid.New(), uuid.New(), "secret", OperationRead); err != nil {
		t.Fatalf("admin: %v", err)
	}
}
