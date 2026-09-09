// Package usermigrate executes checksum-pinned application Go migrations.
package usermigrate

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type Migration struct {
	ID       string
	Revision [32]byte
	Up       func(context.Context, pgx.Tx) error
	Down     func(context.Context, pgx.Tx) error
}

type Entry struct {
	ID          string
	Revision    [32]byte
	Applied     bool
	AppliedAt   time.Time
	ExecutionMS int64
}

type Result struct {
	Migration Entry
	Direction string
	Applied   bool
}

type Validator func(context.Context, pgx.Tx) error
type AfterCommit func(context.Context) error
