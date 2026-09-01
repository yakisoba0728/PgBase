package relation

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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

// ReplayHydratedSequential re-reads a bounded ordered set of base records and
// all of their multi relations in one query. It visits complete records in the
// caller's source order and retains only the record currently being visited.
// The decoder must consume the source collection's ordinary record projection
// and return the canonical ID encoded in that record.
func (b *Bindings) ReplayHydratedSequential(
	ctx context.Context,
	querier RowsQuerier,
	sourceIDs []uuid.UUID,
	decode func(pgx.Row) (uuid.UUID, map[string]any, error),
	visit func(uuid.UUID, map[string]any) bool,
) (visited int, err error) {
	if b == nil || querier == nil || decode == nil || visit == nil {
		return 0, ErrInvalidBinding
	}
	if len(sourceIDs) == 0 {
		return 0, nil
	}
	seen := make(map[uuid.UUID]struct{}, len(sourceIDs))
	for _, id := range sourceIDs {
		if id == uuid.Nil {
			return 0, ErrInvalidBinding
		}
		if _, duplicate := seen[id]; duplicate {
			return 0, ErrInvalidBinding
		}
		seen[id] = struct{}{}
	}
	fields := make([]fieldBinding, 0, len(b.fields))
	for _, field := range b.fields {
		if field.spec.Cardinality == schema.RelationMultiple {
			fields = append(fields, field)
		}
	}
	if len(fields) == 0 {
		return 0, ErrInvalidBinding
	}

	rows, err := querier.Query(ctx, b.replayHydrationStatement(fields), sourceIDs)
	if err != nil {
		return 0, fmt.Errorf("hydrate relation values: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var requestedID uuid.UUID
		var sourceOrdinal int64
		var fieldOrdinals []pgtype.Int4
		var targetIDs []pgtype.UUID
		var positions []pgtype.Int4
		scanner := trailingRowScanner{row: rows, trailing: []any{
			&requestedID, &sourceOrdinal, &fieldOrdinals, &targetIDs, &positions,
		}}
		recordID, record, decodeErr := decode(scanner)
		if decodeErr != nil {
			return visited, fmt.Errorf("hydrate relation values: %w", decodeErr)
		}
		if sourceOrdinal != int64(visited+1) || sourceOrdinal > int64(len(sourceIDs)) ||
			requestedID != sourceIDs[sourceOrdinal-1] || recordID != requestedID || record == nil ||
			len(fieldOrdinals) != len(targetIDs) || len(targetIDs) != len(positions) {
			return visited, ErrInvalidStoredValue
		}

		expectedPositions := make([]int, len(fields))
		for index, ordinal := range fieldOrdinals {
			if !ordinal.Valid || ordinal.Int32 < 0 || int(ordinal.Int32) >= len(fields) ||
				!targetIDs[index].Valid || uuid.UUID(targetIDs[index].Bytes) == uuid.Nil || !positions[index].Valid {
				return visited, ErrInvalidStoredValue
			}
			fieldIndex := int(ordinal.Int32)
			if index > 0 && fieldOrdinals[index-1].Int32 > ordinal.Int32 ||
				int(positions[index].Int32) != expectedPositions[fieldIndex] {
				return visited, ErrInvalidStoredValue
			}
			name := fields[fieldIndex].field.Name
			values, ok := record[name].([]string)
			if !ok {
				return visited, ErrInvalidStoredValue
			}
			record[name] = append(values, uuid.UUID(targetIDs[index].Bytes).String())
			expectedPositions[fieldIndex]++
		}
		for index, field := range fields {
			values, ok := record[field.field.Name].([]string)
			if !ok || expectedPositions[index] != len(values) ||
				len(values) < field.spec.MinSelect || len(values) > field.spec.MaxSelect {
				return visited, ErrInvalidStoredValue
			}
		}
		visited++
		if !visit(recordID, record) {
			rows.Close()
			return visited, rows.Err()
		}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return visited, fmt.Errorf("hydrate relation values: %w", rowsErr)
	}
	if visited != len(sourceIDs) {
		return visited, ErrInvalidStoredValue
	}
	return visited, nil
}

type trailingRowScanner struct {
	row      pgx.Rows
	trailing []any
}

func (s trailingRowScanner) Scan(destinations ...any) error {
	all := make([]any, 0, len(destinations)+len(s.trailing))
	all = append(all, destinations...)
	all = append(all, s.trailing...)
	return s.row.Scan(all...)
}

func (b *Bindings) replayHydrationStatement(fields []fieldBinding) string {
	const rootAlias = "replay_root"
	var statement strings.Builder
	statement.WriteString(`with requested(source_id, source_ordinal) as (
		select source_id, source_ordinal
		from unnest($1::uuid[]) with ordinality as input(source_id, source_ordinal)
	), hydration_edges as (`)
	for index, field := range fields {
		if index > 0 {
			statement.WriteString("\n\t\tunion all")
		}
		statement.WriteString(`
		select edges.source_id, `)
		statement.WriteString(strconv.Itoa(index))
		statement.WriteString(`::integer as field_ordinal, edges.target_id, edges.position
		from `)
		statement.WriteString(field.storageSQL)
		statement.WriteString(` as edges
		join requested on requested.source_id = edges.source_id`)
	}
	statement.WriteString(`
	), hydrated as (
		select source_id,
			array_agg(field_ordinal order by field_ordinal, position, target_id) as field_ordinals,
			array_agg(target_id order by field_ordinal, position, target_id) as target_ids,
			array_agg(position order by field_ordinal, position, target_id) as positions
		from hydration_edges
		group by source_id
	)
	select `)
	statement.WriteString(strings.Join(b.replayRootColumns(rootAlias), ", "))
	statement.WriteString(`, requested.source_id, requested.source_ordinal,
		coalesce(hydrated.field_ordinals, '{}'::integer[]),
		coalesce(hydrated.target_ids, '{}'::uuid[]),
		coalesce(hydrated.positions, '{}'::integer[])
	from requested
	left join `)
	statement.WriteString(b.source.tableSQL)
	statement.WriteString(` as `)
	statement.WriteString(pgx.Identifier{rootAlias}.Sanitize())
	statement.WriteString(` on `)
	statement.WriteString(relationQualified(rootAlias, "id"))
	statement.WriteString(` = requested.source_id
	left join hydrated on hydrated.source_id = requested.source_id
	order by requested.source_ordinal`)
	return statement.String()
}

func (b *Bindings) replayRootColumns(alias string) []string {
	columns := []string{
		relationQualified(alias, "id"),
		relationQualified(alias, "created_at"),
		relationQualified(alias, "updated_at"),
	}
	for _, field := range b.sourceFields {
		if field.Type == schema.TypeRelation && field.Relation != nil && field.Relation.Cardinality == schema.RelationMultiple {
			continue
		}
		column := relationQualified(alias, field.ColumnName)
		if field.Type == schema.TypeNumeric {
			column += "::text"
		}
		columns = append(columns, column)
	}
	return columns
}

func relationQualified(alias, column string) string {
	return pgx.Identifier{alias}.Sanitize() + "." + pgx.Identifier{column}.Sanitize()
}

// HydrateSequential reads a bounded set of sources in one query and visits
// their hydrated multi-relation values in the caller's source order. Only the
// current source's relation arrays are retained; returning false stops before
// later sources are decoded or validated.
func (b *Bindings) HydrateSequential(
	ctx context.Context,
	querier RowsQuerier,
	sourceIDs []uuid.UUID,
	visit func(uuid.UUID, map[string]any) bool,
) (visited int, err error) {
	if b == nil || querier == nil || visit == nil {
		return 0, ErrInvalidBinding
	}
	if len(sourceIDs) == 0 {
		return 0, nil
	}
	seen := make(map[uuid.UUID]struct{}, len(sourceIDs))
	for _, id := range sourceIDs {
		if id == uuid.Nil {
			return 0, ErrInvalidBinding
		}
		if _, duplicate := seen[id]; duplicate {
			return 0, ErrInvalidBinding
		}
		seen[id] = struct{}{}
	}
	fields := make([]fieldBinding, 0, len(b.fields))
	for _, field := range b.fields {
		if field.spec.Cardinality == schema.RelationMultiple {
			fields = append(fields, field)
		}
	}
	if len(fields) == 0 {
		for _, id := range sourceIDs {
			visited++
			if !visit(id, map[string]any{}) {
				break
			}
		}
		return visited, nil
	}

	rows, err := querier.Query(ctx, sequentialHydrationStatement(fields), sourceIDs)
	if err != nil {
		return 0, fmt.Errorf("hydrate relation values: %w", err)
	}
	defer rows.Close()

	var currentOrdinal int64
	var currentID uuid.UUID
	var values map[string]any
	var expectedPositions []int
	lastFieldOrdinal := -1
	sentinelSeen := false
	finishCurrent := func() bool {
		if currentOrdinal == 0 || !sentinelSeen {
			err = ErrInvalidStoredValue
			return false
		}
		for index, field := range fields {
			fieldValues, ok := values[field.field.Name].([]string)
			if !ok || len(fieldValues) < field.spec.MinSelect || len(fieldValues) > field.spec.MaxSelect ||
				expectedPositions[index] != len(fieldValues) {
				err = ErrInvalidStoredValue
				return false
			}
		}
		visited++
		return visit(currentID, values)
	}

	for rows.Next() {
		var sourceID uuid.UUID
		var sourceOrdinal int64
		var fieldOrdinal int
		var targetID pgtype.UUID
		var position pgtype.Int4
		if scanErr := rows.Scan(&sourceID, &sourceOrdinal, &fieldOrdinal, &targetID, &position); scanErr != nil {
			return visited, fmt.Errorf("hydrate relation values: %w", scanErr)
		}
		if sourceOrdinal < 1 || sourceOrdinal > int64(len(sourceIDs)) || sourceID != sourceIDs[sourceOrdinal-1] {
			return visited, ErrInvalidStoredValue
		}
		if sourceOrdinal != currentOrdinal {
			if currentOrdinal != 0 && !finishCurrent() {
				return visited, err
			}
			if sourceOrdinal != currentOrdinal+1 {
				return visited, ErrInvalidStoredValue
			}
			currentOrdinal = sourceOrdinal
			currentID = sourceID
			values = make(map[string]any, len(fields))
			expectedPositions = make([]int, len(fields))
			for _, field := range fields {
				values[field.field.Name] = []string{}
			}
			lastFieldOrdinal = -1
			sentinelSeen = false
		}
		if sourceID != currentID {
			return visited, ErrInvalidStoredValue
		}
		if fieldOrdinal == -1 {
			if sentinelSeen || targetID.Valid || position.Valid || lastFieldOrdinal != -1 {
				return visited, ErrInvalidStoredValue
			}
			sentinelSeen = true
			continue
		}
		if !sentinelSeen || fieldOrdinal < 0 || fieldOrdinal >= len(fields) ||
			fieldOrdinal < lastFieldOrdinal || !targetID.Valid || !position.Valid ||
			uuid.UUID(targetID.Bytes) == uuid.Nil || int(position.Int32) != expectedPositions[fieldOrdinal] {
			return visited, ErrInvalidStoredValue
		}
		valuesForField, ok := values[fields[fieldOrdinal].field.Name].([]string)
		if !ok {
			return visited, ErrInvalidStoredValue
		}
		values[fields[fieldOrdinal].field.Name] = append(valuesForField, uuid.UUID(targetID.Bytes).String())
		expectedPositions[fieldOrdinal]++
		lastFieldOrdinal = fieldOrdinal
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return visited, fmt.Errorf("hydrate relation values: %w", rowsErr)
	}
	if currentOrdinal != int64(len(sourceIDs)) {
		return visited, ErrInvalidStoredValue
	}
	if !finishCurrent() {
		return visited, err
	}
	return visited, nil
}

func sequentialHydrationStatement(fields []fieldBinding) string {
	var statement strings.Builder
	statement.WriteString(`with requested(source_id, source_ordinal) as (
		select source_id, source_ordinal
		from unnest($1::uuid[]) with ordinality as input(source_id, source_ordinal)
	), hydration_rows as (
		select requested.source_id, requested.source_ordinal,
			-1::integer as field_ordinal, null::uuid as target_id, null::integer as position
		from requested`)
	for index, field := range fields {
		statement.WriteString(`
		union all
		select requested.source_id, requested.source_ordinal, `)
		statement.WriteString(strconv.Itoa(index))
		statement.WriteString(`::integer, edges.target_id, edges.position
		from requested
		join `)
		statement.WriteString(field.storageSQL)
		statement.WriteString(` as edges on edges.source_id = requested.source_id`)
	}
	statement.WriteString(`
	)
	select source_id, source_ordinal, field_ordinal, target_id, position
	from hydration_rows
	order by source_ordinal, field_ordinal, position`)
	return statement.String()
}

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
