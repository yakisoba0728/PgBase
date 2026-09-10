package migrations

import (
	"context"
	_ "embed"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/pgbase"
)

//go:embed 20260909000000_add_widgets.go
var source20260909000000 []byte

func init() {
	pgbase.RegisterUserMigration(pgbase.UserMigration{
		ID: "20260909000000_add_widgets", Source: source20260909000000,
		Up: func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `create table public.widgets(id uuid primary key, label text not null)`)
			return err
		},
		Down: func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `drop table public.widgets`)
			return err
		},
	})
}
