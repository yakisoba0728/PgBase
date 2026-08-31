package relation

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

// ReplaceMulti replaces every present multi relation in field-ordinal order.
// Omitted patch values are untouched and an explicit empty array clears all
// edges. Positions are the input array's zero-based order. The caller owns the
// transaction and must acquire collection/row locks first.
func (b *Bindings) ReplaceMulti(ctx context.Context, tx pgx.Tx, sourceID uuid.UUID, input Input) error {
	if tx == nil || sourceID == uuid.Nil || !b.accepts(input) {
		return ErrInvalidBinding
	}
	for index, value := range input.relations {
		field := b.fields[index]
		if !value.Present || field.spec.Cardinality != schema.RelationMultiple {
			continue
		}
		if _, err := tx.Exec(ctx, "delete from "+field.storageSQL+" where source_id = $1", sourceID); err != nil {
			return fmt.Errorf("replace relation values: %w", err)
		}
		if len(value.IDs) == 0 {
			continue
		}
		if _, err := tx.Exec(ctx, "insert into "+field.storageSQL+` (source_id, target_id, position)
			select $1, target_id, (ordinality - 1)::integer
			from unnest($2::uuid[]) with ordinality as targets(target_id, ordinality)`, sourceID, value.IDs); err != nil {
			return fmt.Errorf("replace relation values: %w", err)
		}
	}
	return nil
}

// RowsQuerier is the minimal read surface Hydrate needs. Both pgx.Tx and the
// repository database pool satisfy it.
type RowsQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// ErrHydrationBudgetExceeded reports that HydrateBounded observed one more
// junction row than the caller's remaining budget. Its text deliberately
// excludes relation values and physical storage identifiers.
var ErrHydrationBudgetExceeded = errors.New("relation hydration budget exceeded")

// Hydrate fills every multi-relation field in records with []string values.
// It executes exactly one ordered query per multi field (and none when there
// are no source IDs), avoiding both N+1 reads and accidental null arrays.
func (b *Bindings) Hydrate(
	ctx context.Context,
	querier RowsQuerier,
	sourceIDs []uuid.UUID,
	records map[uuid.UUID]map[string]any,
) error {
	_, err := b.hydrate(ctx, querier, sourceIDs, records, -1, nil)
	return err
}

// HydrateBounded fills multi-relation fields while reading at most remaining
// junction rows plus one overflow probe. Used is the number of observed rows,
// including that probe when ErrHydrationBudgetExceeded is returned. It
// executes one ordered query per multi field and callers can account for those
// query steps from the bound catalog field count. Records may be partially
// hydrated on any error and must then be discarded by the caller.
func (b *Bindings) HydrateBounded(
	ctx context.Context,
	querier RowsQuerier,
	sourceIDs []uuid.UUID,
	records map[uuid.UUID]map[string]any,
	remaining int,
) (used int, err error) {
	if remaining < 0 {
		return 0, ErrInvalidBinding
	}
	return b.hydrate(ctx, querier, sourceIDs, records, remaining, nil)
}

// HydrateFieldsBounded is the projection-aware read facet. It hydrates only
// the named multi-relation fields, in catalog ordinal order, so a caller's
// field projection cannot spend row or query budgets on hidden junctions.
// Unknown, duplicate, or non-multi field names fail closed before any query.
func (b *Bindings) HydrateFieldsBounded(
	ctx context.Context,
	querier RowsQuerier,
	sourceIDs []uuid.UUID,
	records map[uuid.UUID]map[string]any,
	fieldNames []string,
	remaining int,
) (used int, err error) {
	if remaining < 0 {
		return 0, ErrInvalidBinding
	}
	fields, err := b.selectedMultiFields(fieldNames)
	if err != nil {
		return 0, err
	}
	return b.hydrate(ctx, querier, sourceIDs, records, remaining, fields)
}

// hydrate uses a negative remaining value only for the legacy unbounded
// Hydrate wrapper. HydrateBounded rejects negative budgets at its interface.
func (b *Bindings) hydrate(
	ctx context.Context,
	querier RowsQuerier,
	sourceIDs []uuid.UUID,
	records map[uuid.UUID]map[string]any,
	remaining int,
	selected []fieldBinding,
) (used int, err error) {
	if b == nil || querier == nil || records == nil {
		return 0, ErrInvalidBinding
	}
	ids := uniqueSortedUUIDs(sourceIDs)
	if len(ids) == 0 {
		return 0, nil
	}
	for _, id := range ids {
		if id == uuid.Nil {
			return 0, ErrInvalidBinding
		}
		if records[id] == nil {
			records[id] = make(map[string]any)
		}
	}

	requested := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		requested[id] = struct{}{}
	}
	fields := selected
	if fields == nil {
		fields = b.fields
	}
	for _, field := range fields {
		if field.spec.Cardinality != schema.RelationMultiple {
			continue
		}
		for _, id := range ids {
			records[id][field.field.Name] = []string{}
		}

		statement := "select source_id, target_id, position from " + field.storageSQL +
			" where source_id = any($1::uuid[]) order by source_id, position"
		arguments := []any{ids}
		if remaining >= 0 {
			available := remaining - used
			if available < int(^uint(0)>>1) {
				statement += " limit $2"
				arguments = append(arguments, available+1)
			}
		}
		rows, err := querier.Query(ctx, statement, arguments...)
		if err != nil {
			return used, fmt.Errorf("hydrate relation values: %w", err)
		}
		expectedPosition := make(map[uuid.UUID]int, len(ids))
		for rows.Next() {
			used++
			if remaining >= 0 && used > remaining {
				rows.Close()
				return used, ErrHydrationBudgetExceeded
			}
			var sourceID, targetID uuid.UUID
			var position int
			if err := rows.Scan(&sourceID, &targetID, &position); err != nil {
				rows.Close()
				return used, fmt.Errorf("hydrate relation values: %w", err)
			}
			if _, ok := requested[sourceID]; !ok || targetID == uuid.Nil || position != expectedPosition[sourceID] {
				rows.Close()
				return used, ErrInvalidStoredValue
			}
			values, ok := records[sourceID][field.field.Name].([]string)
			if !ok {
				rows.Close()
				return used, ErrInvalidStoredValue
			}
			records[sourceID][field.field.Name] = append(values, targetID.String())
			expectedPosition[sourceID]++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return used, fmt.Errorf("hydrate relation values: %w", err)
		}
		rows.Close()
		for _, id := range ids {
			values, ok := records[id][field.field.Name].([]string)
			if !ok || len(values) < field.spec.MinSelect || len(values) > field.spec.MaxSelect {
				return used, ErrInvalidStoredValue
			}
		}
	}
	return used, nil
}

func (b *Bindings) selectedMultiFields(fieldNames []string) ([]fieldBinding, error) {
	if b == nil || b.fieldsByName == nil {
		return nil, ErrInvalidBinding
	}
	selected := make(map[int]struct{}, len(fieldNames))
	for _, name := range fieldNames {
		index, ok := b.fieldsByName[name]
		if !ok || index < 0 || index >= len(b.fields) ||
			b.fields[index].spec.Cardinality != schema.RelationMultiple {
			return nil, ErrInvalidBinding
		}
		if _, duplicate := selected[index]; duplicate {
			return nil, ErrInvalidBinding
		}
		selected[index] = struct{}{}
	}
	fields := make([]fieldBinding, 0, len(selected))
	for index, field := range b.fields {
		if _, ok := selected[index]; ok {
			fields = append(fields, field)
		}
	}
	return fields, nil
}

func uniqueSortedUUIDs(values []uuid.UUID) []uuid.UUID {
	unique := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		unique[value] = struct{}{}
	}
	result := make([]uuid.UUID, 0, len(unique))
	for value := range unique {
		result = append(result, value)
	}
	sortUUIDs(result)
	return result
}
