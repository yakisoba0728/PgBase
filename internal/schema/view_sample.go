package schema

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"strings"
)

const MaximumViewSampleQueryBytes = 5000

type ViewSample struct {
	Fields       []PlannedField   `json:"fields"`
	Items        []map[string]any `json:"items"`
	Dependencies []uuid.UUID      `json:"dependencies"`
}

func (s *Service) SampleView(ctx context.Context, query string) (result ViewSample, err error) {
	query = strings.TrimSpace(query)
	if len(query) == 0 || len(query) > MaximumViewSampleQueryBytes {
		return result, validation("query", "invalid")
	}
	err = s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		store := NewStore(tx)
		version, e := store.SchemaVersion(ctx)
		if e != nil {
			return e
		}
		catalog, e := store.ReadCatalog(ctx, version)
		if e != nil {
			return e
		}
		inspection, e := InspectSQLView(ctx, tx, catalog, query)
		if e != nil {
			return e
		}
		rows, e := tx.Query(ctx, "select * from ("+strings.TrimSuffix(query, ";")+") as pgbase_sample limit 10", pgx.QueryExecModeExec)
		if e != nil {
			return ErrConflict
		}
		defer rows.Close()
		descriptions := rows.FieldDescriptions()
		for rows.Next() {
			values, e := rows.Values()
			if e != nil {
				return e
			}
			item := make(map[string]any, len(values))
			for i, value := range values {
				item[string(descriptions[i].Name)] = value
			}
			result.Items = append(result.Items, item)
		}
		if e := rows.Err(); e != nil {
			return e
		}
		result.Fields, result.Dependencies = inspection.Fields, inspection.Dependencies
		return nil
	})
	return result, err
}
