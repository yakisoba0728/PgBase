package database

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestClassifyPostgreSQLErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want Kind
	}{
		{name: "unique violation is a conflict", err: &pgconn.PgError{Code: "23505"}, want: KindConflict},
		{name: "not null violation is validation", err: &pgconn.PgError{Code: "23502"}, want: KindValidation},
		{name: "check violation is validation", err: &pgconn.PgError{Code: "23514"}, want: KindValidation},
		{name: "invalid text representation is validation", err: &pgconn.PgError{Code: "22P02"}, want: KindValidation},
		{name: "lock unavailable is unavailable", err: &pgconn.PgError{Code: "55P03"}, want: KindUnavailable},
		{name: "query cancellation is unavailable", err: &pgconn.PgError{Code: "57014"}, want: KindUnavailable},
		{name: "administrator shutdown is unavailable", err: &pgconn.PgError{Code: "57P01"}, want: KindUnavailable},
		{name: "serialization failure is retryable", err: &pgconn.PgError{Code: "40001"}, want: KindUnavailable},
		{name: "deadlock is retryable", err: &pgconn.PgError{Code: "40P01"}, want: KindUnavailable},
		{name: "connection SQLSTATE is unavailable", err: &pgconn.PgError{Code: "08006"}, want: KindUnavailable},
		{name: "unexpected EOF is unavailable", err: io.ErrUnexpectedEOF, want: KindUnavailable},
		{name: "network error is unavailable", err: &net.OpError{Op: "read", Net: "tcp", Err: io.EOF}, want: KindUnavailable},
		{name: "wrapped context cancellation is canceled", err: fmtWrap(context.Canceled), want: KindCanceled},
		{name: "unknown error stays unknown", err: errors.New("unknown"), want: KindUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.err); got != tt.want {
				t.Fatalf("Classify(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func fmtWrap(err error) error {
	return fmt.Errorf("operation: %w", err)
}
