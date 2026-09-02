package relation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const collectionAdvisoryDomain = "pgbase:relation-collection-lock:v1"

// lockCollections acquires transaction-scoped exclusive advisory locks for
// the source collection and every target collection changed by input. UUID
// ordering is global and independent of JSON field/value order.
func (b *Bindings) lockCollections(ctx context.Context, tx pgx.Tx, input writeInput) error {
	if tx == nil || !b.accepts(input) {
		return ErrInvalidBinding
	}
	ids := b.collectionLockIDs(input)
	for _, id := range ids {
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", collectionAdvisoryKey(id)); err != nil {
			return fmt.Errorf("lock relation collections: %w", err)
		}
	}
	return nil
}

func (b *Bindings) collectionLockIDs(input writeInput) []uuid.UUID {
	unique := map[uuid.UUID]struct{}{b.source.id: {}}
	for _, value := range input.relations {
		if value.Present {
			unique[value.targetCollectionID] = struct{}{}
		}
	}
	ids := make([]uuid.UUID, 0, len(unique))
	for id := range unique {
		ids = append(ids, id)
	}
	sortUUIDs(ids)
	return ids
}

func collectionAdvisoryKey(id uuid.UUID) int64 {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(collectionAdvisoryDomain))
	_, _ = hasher.Write(id[:])
	digest := hasher.Sum(nil)
	return int64(binary.BigEndian.Uint64(digest[:8]))
}

type rowLock struct {
	collectionID uuid.UUID
	recordID     uuid.UUID
	tableSQL     string
	update       bool
	fields       []string
}

// lockRows locks a stored source FOR UPDATE and all referenced targets FOR KEY
// SHARE. With sourcePending, the not-yet-inserted source is not locked and its
// own ID is accepted as a self target. Missing targets are deliberately merged
// into one field/code shape with no target ID or table disclosure.
//
// Callers must call lockCollections first in the same transaction.
func (b *Bindings) lockRows(
	ctx context.Context,
	tx pgx.Tx,
	sourceID uuid.UUID,
	state sourceState,
	input writeInput,
) error {
	if tx == nil || sourceID == uuid.Nil || !b.accepts(input) ||
		(state != sourcePending && state != sourceStored) {
		return ErrInvalidBinding
	}

	locks := b.rowLocks(sourceID, state, input)
	sourceMissing := false
	missingFields := make(map[string]struct{})
	for first := 0; first < len(locks); {
		if locks[first].update {
			var found uuid.UUID
			err := tx.QueryRow(ctx, "select id from "+locks[first].tableSQL+" where id = $1 FOR UPDATE", locks[first].recordID).Scan(&found)
			switch {
			case err == nil:
			case errors.Is(err, pgx.ErrNoRows):
				sourceMissing = true
			default:
				return fmt.Errorf("lock relation rows: %w", err)
			}
			first++
			continue
		}

		// The sorted sequence can contain at most two KEY SHARE batches for
		// the source collection (on either side of its UPDATE lock), and one
		// batch for every other collection.
		last := first + 1
		for last < len(locks) && !locks[last].update && locks[last].collectionID == locks[first].collectionID {
			last++
		}
		missing, err := lockTargetBatch(ctx, tx, locks[first:last])
		if err != nil {
			return err
		}
		for _, lock := range missing {
			for _, field := range lock.fields {
				missingFields[field] = struct{}{}
			}
		}
		first = last
	}
	if sourceMissing {
		return ErrSourceNotFound
	}
	if len(missingFields) != 0 {
		for _, field := range b.fields {
			if _, missing := missingFields[field.field.Name]; missing {
				return validation(field.field.Name, "relation_target_invalid")
			}
		}
		return validation("relation", "relation_target_invalid")
	}
	return nil
}

func lockTargetBatch(ctx context.Context, tx pgx.Tx, locks []rowLock) ([]rowLock, error) {
	ids := make([]uuid.UUID, len(locks))
	requested := make(map[uuid.UUID]rowLock, len(locks))
	for index, lock := range locks {
		ids[index] = lock.recordID
		requested[lock.recordID] = lock
	}
	rows, err := tx.Query(ctx, "select id from "+locks[0].tableSQL+
		" where id = any($1::uuid[]) order by id FOR KEY SHARE", ids)
	if err != nil {
		return nil, fmt.Errorf("lock relation rows: %w", err)
	}
	for rows.Next() {
		var found uuid.UUID
		if err := rows.Scan(&found); err != nil {
			rows.Close()
			return nil, fmt.Errorf("lock relation rows: %w", err)
		}
		if _, expected := requested[found]; !expected {
			rows.Close()
			return nil, ErrInvalidStoredValue
		}
		delete(requested, found)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("lock relation rows: %w", err)
	}
	rows.Close()
	missing := make([]rowLock, 0, len(requested))
	for _, lock := range locks {
		if _, absent := requested[lock.recordID]; absent {
			missing = append(missing, lock)
		}
	}
	return missing, nil
}

func (b *Bindings) rowLocks(sourceID uuid.UUID, state sourceState, input writeInput) []rowLock {
	byPair := make(map[[2]uuid.UUID]*rowLock)
	if state == sourceStored {
		key := [2]uuid.UUID{b.source.id, sourceID}
		byPair[key] = &rowLock{
			collectionID: b.source.id,
			recordID:     sourceID,
			tableSQL:     b.source.tableSQL,
			update:       true,
		}
	}
	for _, value := range input.relations {
		if !value.Present {
			continue
		}
		target := b.collections[value.targetCollectionID]
		for _, targetID := range value.IDs {
			if state == sourcePending && target.id == b.source.id && targetID == sourceID {
				continue
			}
			key := [2]uuid.UUID{target.id, targetID}
			lock, exists := byPair[key]
			if !exists {
				lock = &rowLock{
					collectionID: target.id,
					recordID:     targetID,
					tableSQL:     target.tableSQL,
				}
				byPair[key] = lock
			}
			if !containsString(lock.fields, value.FieldName) {
				lock.fields = append(lock.fields, value.FieldName)
			}
		}
	}
	locks := make([]rowLock, 0, len(byPair))
	for _, lock := range byPair {
		sort.Strings(lock.fields)
		locks = append(locks, *lock)
	}
	sort.Slice(locks, func(i, j int) bool {
		if comparison := bytes.Compare(locks[i].collectionID[:], locks[j].collectionID[:]); comparison != 0 {
			return comparison < 0
		}
		return bytes.Compare(locks[i].recordID[:], locks[j].recordID[:]) < 0
	})
	return locks
}

func sortUUIDs(ids []uuid.UUID) {
	sort.Slice(ids, func(i, j int) bool { return bytes.Compare(ids[i][:], ids[j][:]) < 0 })
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
