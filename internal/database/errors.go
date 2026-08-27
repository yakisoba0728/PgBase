package database

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// Kind is a stable category for database errors exposed by pgBase.
type Kind uint8

const (
	KindUnknown Kind = iota
	KindValidation
	KindConflict
	KindUnavailable
	KindCanceled
)

// Classify maps PostgreSQL and context errors to pgBase error kinds.
func Classify(err error) Kind {
	if err == nil {
		return KindUnknown
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return KindCanceled
	}

	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return KindUnknown
	}

	switch postgresError.Code {
	case "23505":
		return KindConflict
	case "23502", "23514", "22P02":
		return KindValidation
	case "55P03", "57014", "57P01":
		return KindUnavailable
	default:
		return KindUnknown
	}
}
