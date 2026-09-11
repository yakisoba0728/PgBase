package filestore

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPolicyCallbackMustExplicitlyOptIntoPublicFiles(t *testing.T) {
	service := Service{options: Options{MaximumBytes: 1, AllowedTypes: []string{"text/plain"}, Policy: func(context.Context, pgx.Tx, uuid.UUID, string) (FieldPolicy, error) { return FieldPolicy{}, nil }}}
	policy, err := service.policy(context.Background(), nil, uuid.New(), "file")
	if err != nil || policy.Protected == nil || !*policy.Protected {
		t.Fatalf("zero-value policy exposed file: %#v, %v", policy, err)
	}
	public := false
	service.options.Policy = func(context.Context, pgx.Tx, uuid.UUID, string) (FieldPolicy, error) {
		return FieldPolicy{Protected: &public}, nil
	}
	policy, err = service.policy(context.Background(), nil, uuid.New(), "file")
	if err != nil || policy.Protected == nil || *policy.Protected {
		t.Fatalf("explicit public policy ignored: %#v, %v", policy, err)
	}
}
