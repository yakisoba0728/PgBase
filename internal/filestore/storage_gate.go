package filestore

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type storageOperationGate interface {
	BeginStorageOperation(context.Context) (context.Context, func(), error)
}

type storagePublicationGate interface {
	GuardStoragePublication(context.Context, pgx.Tx) error
}

func (s *Service) beginStorageOperation(ctx context.Context) (context.Context, func(), error) {
	if gated, ok := s.objects.(storageOperationGate); ok {
		return gated.BeginStorageOperation(ctx)
	}
	return ctx, func() {}, nil
}

func (s *Service) guardStoragePublication(ctx context.Context, tx pgx.Tx) error {
	if gated, ok := s.objects.(storagePublicationGate); ok {
		return gated.GuardStoragePublication(ctx, tx)
	}
	return nil
}
