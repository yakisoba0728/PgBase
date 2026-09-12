// Package usermigrate executes checksum-pinned application Go migrations.
package usermigrate

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type Migration struct {
	ID                 string
	Revision           [32]byte
	BeforeSnapshot     []byte
	AfterSnapshot      []byte
	IrreversibleReason string
	Up                 func(context.Context, pgx.Tx) error
	Down               func(context.Context, pgx.Tx) error
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

type HistoryState string

const (
	HistoryApplied          HistoryState = "applied"
	HistoryPending          HistoryState = "pending"
	HistoryChecksumMismatch HistoryState = "checksum_mismatch"
	HistoryOnly             HistoryState = "history_only"
	HistorySourceGap        HistoryState = "source_gap"
)

type HistoryEntry struct {
	ID              string
	State           HistoryState
	SourceRevision  *[32]byte
	AppliedRevision []byte
	AppliedAt       time.Time
	ExecutionMS     int64
}

// HistoryCleanup is the deterministic result of removing applied ledger rows
// whose migration source is no longer compiled into this executable.
// Cleanup never runs migration callbacks and never changes schema versions.
type HistoryCleanup struct {
	Entries []HistoryEntry
	DryRun  bool
}

type Validator func(context.Context, pgx.Tx) error
type AfterCommit func(context.Context) error
