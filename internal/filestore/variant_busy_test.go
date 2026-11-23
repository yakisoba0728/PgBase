package filestore

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yakisoba0728/PgBase/internal/database"
)

func TestVariantBusyClassifiesContention(t *testing.T) {
	live := context.Background()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name string
		ctx  context.Context
		err  error
		busy bool
	}{
		{name: "lock timeout", ctx: live, err: &pgconn.PgError{Code: "55P03"}, busy: true},
		{name: "statement timeout", ctx: live, err: fmt.Errorf("lock file row: %w", &pgconn.PgError{Code: "57014"}), busy: true},
		{name: "too many connections", ctx: live, err: &pgconn.PgError{Code: "53300"}, busy: true},
		{name: "backup gate", ctx: live, err: database.ErrBackupGateBusy, busy: true},
		{name: "decode memory", ctx: live, err: ErrImageBusy, busy: true},
		{name: "caller canceled", ctx: canceled, err: &pgconn.PgError{Code: "57014"}},
		{name: "not found", ctx: live, err: ErrNotFound},
		{name: "other database error", ctx: live, err: &pgconn.PgError{Code: "23505"}},
		{name: "storage fault", ctx: live, err: errors.New("disk failed")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := variantBusy(test.ctx, test.err)
			if errors.Is(got, ErrImageBusy) != test.busy {
				t.Fatalf("variantBusy(%v) = %v, busy want %t", test.err, got, test.busy)
			}
			if !errors.Is(got, test.err) {
				t.Fatalf("variantBusy(%v) = %v, lost the cause", test.err, got)
			}
		})
	}
	if variantBusy(live, nil) != nil {
		t.Fatal("variantBusy(nil) must stay nil")
	}
}
