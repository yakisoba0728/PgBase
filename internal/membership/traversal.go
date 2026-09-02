package membership

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Traversal is the durable membership view for one collection in the caller's
// transaction. Its transaction time is the PostgreSQL transaction timestamp.
type Traversal struct {
	TransactionTime time.Time
	Generation      int64
	HighWater       int64
	IssueVersion    int16
	V4Retired       bool

	ready bool
	state State
}

// ValidateV5 admits a signed v5 traversal against this transaction's durable
// ready generation and current collection high-water. A lower high-water is a
// valid earlier snapshot; a higher one could not have been issued by pgBase.
func (t Traversal) ValidateV5(generation, highWater int64) error {
	if !t.ready || t.Generation < 1 || t.HighWater < 0 || generation < 1 || highWater < 0 ||
		generation != t.Generation || highWater > t.HighWater {
		return ErrTraversalInvalid
	}
	return nil
}

const traversalResolutionSelect = `with pinned_schema as materialized (
	select version as schema_version
	from pgbase_meta.schema_state
	where singleton
	limit 1
	for share
), capability_state as materialized (
	select ` + capabilityColumns + `
	from pinned_schema
	join pgbase_meta.record_membership_capability as capability
	  on capability.singleton
	where pinned_schema.schema_version >= 0
	limit 1
), collection_state as materialized (
	select capability_state.*,
		creation_counter.generation as counter_generation,
		creation_counter.last_ordinal
	from capability_state
	join pgbase_meta.collection_creation_counters as creation_counter
	  on creation_counter.collection_id = $1
	limit 1
), known_migrations(version, checksum_hex, ordinal) as (
	select known.version, known.checksum_hex, known.ordinal
	from unnest($2::bigint[], $3::text[]) with ordinality
		as known(version, checksum_hex, ordinal)
), applied_migrations as materialized (
	select migration.version,
		migration.checksum,
		row_number() over (order by migration.version) as ordinal
	from pinned_schema
	join pgbase_meta.system_migrations as migration on true
	order by migration.version
	limit $4
)
select ` + capabilityColumns + `,
	transaction_timestamp(),
	counter_generation,
	last_ordinal,
	to_regclass('pgbase_meta.record_membership_capability') is not null,
	to_regclass('pgbase_meta.collection_creation_counters') is not null,
	to_regclass('pgbase_meta.record_creation_ordinals') is not null,
	to_regclass('pgbase_meta.record_membership_backfills') is not null,
	to_regclass('pgbase_meta.runtime_writer_leases') is not null,
	(select count(*) from applied_migrations),
	coalesce((
		select bool_and(coalesce(
			applied.version = known.version
			and encode(applied.checksum, 'hex') = known.checksum_hex,
			false))
		from applied_migrations as applied
		left join known_migrations as known using (ordinal)
	), false)
from collection_state`

// ResolveTraversal returns the complete durable membership decision needed by
// a query transaction. The schema pin, installation/history validation,
// capability read, collection high-water, and PostgreSQL transaction time are
// deliberately acquired by one dependency-ordered statement.
func ResolveTraversal(
	ctx context.Context,
	tx pgx.Tx,
	collectionID uuid.UUID,
) (Traversal, error) {
	if tx == nil || collectionID == uuid.Nil {
		return Traversal{}, traversalUnavailable(ErrInvalidState)
	}
	versions, checksums, err := currentWriterMigrationHistory()
	if err != nil {
		return Traversal{}, traversalUnavailable(err)
	}
	var (
		transactionTime                   time.Time
		counterGeneration, lastOrdinal    int64
		capability, counters, ordinals    bool
		backfills, leases, historyMatches bool
		historyCount                      int64
	)
	status, err := scanStatusWithTrailing(
		tx.QueryRow(
			ctx,
			traversalResolutionSelect,
			collectionID,
			versions,
			checksums,
			len(versions)+1,
		),
		&transactionTime,
		&counterGeneration,
		&lastOrdinal,
		&capability,
		&counters,
		&ordinals,
		&backfills,
		&leases,
		&historyCount,
		&historyMatches,
	)
	if err != nil {
		return Traversal{}, traversalUnavailable(err)
	}
	present := 0
	for _, exists := range []bool{capability, counters, ordinals, backfills, leases} {
		if exists {
			present++
		}
	}
	if present != 5 || historyCount < 7 || historyCount > int64(len(versions)) || !historyMatches ||
		transactionTime.IsZero() || !validTraversalStatus(status) ||
		counterGeneration != status.Generation || lastOrdinal < 0 ||
		(status.State == StateOff && lastOrdinal != 0) || status.State == StateCutover {
		return Traversal{}, traversalUnavailable(ErrInvalidState)
	}
	return Traversal{
		TransactionTime: transactionTime.UTC(),
		Generation:      status.Generation,
		HighWater:       lastOrdinal,
		IssueVersion:    status.CursorIssueVersion,
		V4Retired:       status.V4RetiredAt != nil,
		ready:           status.State == StateReady,
		state:           status.State,
	}, nil
}

func validTraversalStatus(status Status) bool {
	if status.UpdatedAt.IsZero() || status.Generation < 0 ||
		(status.CursorIssueVersion != 4 && status.CursorIssueVersion != 5) {
		return false
	}
	cutoverCounts := status.cutoverCollections != nil && *status.cutoverCollections >= 0 &&
		status.cutoverRecords != nil && *status.cutoverRecords >= 0
	readyCount := status.readyCollections != nil && *status.readyCollections >= 0
	if status.CutoverAt != nil && status.UpdatedAt.Before(*status.CutoverAt) ||
		status.ReadyAt != nil && (status.CutoverAt == nil || status.ReadyAt.Before(*status.CutoverAt) ||
			status.UpdatedAt.Before(*status.ReadyAt)) ||
		status.V5EnabledAt != nil && (status.ReadyAt == nil || status.V5EnabledAt.Before(*status.ReadyAt) ||
			status.UpdatedAt.Before(*status.V5EnabledAt)) ||
		status.DecodeV4Until != nil && (status.V5EnabledAt == nil || status.DecodeV4Until.Before(*status.V5EnabledAt)) ||
		status.V4RetiredAt != nil && (status.DecodeV4Until == nil || status.V4RetiredAt.Before(*status.DecodeV4Until) ||
			status.UpdatedAt.Before(*status.V4RetiredAt)) {
		return false
	}
	switch status.State {
	case StateOff:
		return status.Generation == 0 && status.MinimumWriterVersion == WriterA0 &&
			status.CursorIssueVersion == 4 && status.CutoverAt == nil &&
			status.cutoverCollections == nil && status.cutoverRecords == nil &&
			status.ReadyAt == nil && status.readyCollections == nil &&
			status.V5EnabledAt == nil && status.DecodeV4Until == nil && status.V4RetiredAt == nil
	case StateCutover:
		return status.Generation >= 1 && status.MinimumWriterVersion == WriterA &&
			status.CursorIssueVersion == 4 && status.CutoverAt != nil &&
			status.cutoverCollections == nil && status.cutoverRecords == nil &&
			status.ReadyAt == nil && status.readyCollections == nil &&
			status.V5EnabledAt == nil && status.DecodeV4Until == nil && status.V4RetiredAt == nil
	case StateDualWrite:
		return status.Generation >= 1 && status.MinimumWriterVersion == WriterA &&
			status.CursorIssueVersion == 4 && status.CutoverAt != nil && cutoverCounts &&
			status.ReadyAt == nil && status.readyCollections == nil &&
			status.V5EnabledAt == nil && status.DecodeV4Until == nil && status.V4RetiredAt == nil
	case StateReady:
		// The two collection counts are receipts stamped at cutover and at
		// promotion, not a pair that has to agree: a collection created in the
		// backfill window is counted only by the later one. Backfill
		// completeness is proven by PromoteReady, which refuses to promote
		// unless every catalog collection has a validated ready backfill row.
		if status.Generation < 1 || status.CutoverAt == nil || !cutoverCounts ||
			status.ReadyAt == nil || !readyCount {
			return false
		}
		switch status.CursorIssueVersion {
		case 4:
			return status.MinimumWriterVersion == WriterA && status.V5EnabledAt == nil &&
				status.DecodeV4Until == nil && status.V4RetiredAt == nil
		case 5:
			return status.MinimumWriterVersion == WriterB && status.V5EnabledAt != nil &&
				status.DecodeV4Until != nil
		}
	}
	return false
}

func traversalUnavailable(cause error) error {
	return &traversalUnavailableError{cause: cause}
}

type traversalUnavailableError struct {
	cause error
}

func (*traversalUnavailableError) Error() string { return ErrUnavailable.Error() }

func (e *traversalUnavailableError) Unwrap() []error {
	return []error{ErrUnavailable, e.cause}
}
