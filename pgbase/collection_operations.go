package pgbase

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordops"
)

type CollectionTruncateRequest struct {
	Name            string
	AdministratorID uuid.UUID
	RequestID       string
}

type CollectionTruncateResult = recordops.TruncateResult

// TruncateCollection is the native counterpart of the administrator HTTP and
// JavaScript APIs. It runs every record through the ordinary delete lifecycle.
func (a *App) TruncateCollection(ctx context.Context, request CollectionTruncateRequest) (CollectionTruncateResult, error) {
	if a == nil || a.records == nil {
		return CollectionTruncateResult{}, errors.New("record service is unavailable")
	}
	return a.records.Truncate(ctx, recordops.TruncateRequest{
		Principal: identity.Subject{Kind: identity.Administrator, ID: request.AdministratorID},
		RequestID: request.RequestID, Collection: request.Name,
	})
}
