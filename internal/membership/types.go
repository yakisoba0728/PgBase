// Package membership owns the durable record-creation membership rollout.
package membership

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type State string

const (
	StateOff       State = "off"
	StateCutover   State = "cutover"
	StateDualWrite State = "dual_write"
	StateReady     State = "ready"
)

type WriterVersion int16

const (
	WriterA0 WriterVersion = iota
	WriterA
	WriterB
	WriterC
)

const CursorTTL = 15 * time.Minute

var (
	ErrUnavailable        = errors.New("record membership is unavailable")
	ErrInvalidState       = errors.New("invalid record membership state")
	ErrWriterTooOld       = errors.New("record writer version is no longer accepted")
	ErrWriterFleetTooOld  = errors.New("active record writer fleet is too old")
	ErrBackfillIncomplete = errors.New("record membership backfill is incomplete")
	ErrMembershipDrift    = errors.New("record membership validation failed")
	ErrCursorDrainPending = errors.New("legacy cursor drain is incomplete")
	ErrTraversalInvalid   = errors.New("record membership traversal is invalid")
)

type Status struct {
	State                State
	Generation           int64
	MinimumWriterVersion WriterVersion
	CursorIssueVersion   int16
	CutoverAt            *time.Time
	cutoverCollections   *int64
	cutoverRecords       *int64
	ReadyAt              *time.Time
	readyCollections     *int64
	V5EnabledAt          *time.Time
	DecodeV4Until        *time.Time
	V4RetiredAt          *time.Time
	UpdatedAt            time.Time
}

type CutoverReceipt struct {
	Generation  int64
	Collections int
	Records     int64
	CutoverAt   time.Time
}

// FleetProof is the operator-confirmed active deployment observed before an
// irreversible rollout transition. The transition re-reads the durable lease
// set under its database locks and requires an exact match.
type FleetProof struct {
	Members []FleetMember
}

type FleetMember struct {
	InstanceID uuid.UUID
	Version    WriterVersion
}

type BackfillProgress struct {
	Generation int64
	Collection uuid.UUID
	Scanned    int
	Inserted   int
	Ready      bool
	Done       bool
}

type ReadyReceipt struct {
	Generation  int64
	Collections int
	ReadyAt     time.Time
}

type ActivationReceipt struct {
	Generation           int64
	MinimumWriterVersion WriterVersion
	CursorIssueVersion   int16
	EnabledAt            time.Time
	DecodeV4Until        time.Time
}

type RollbackReceipt struct {
	Generation           int64
	MinimumWriterVersion WriterVersion
	CursorIssueVersion   int16
	RolledBackAt         time.Time
}

type RetirementReceipt struct {
	Generation int64
	RetiredAt  time.Time
}
