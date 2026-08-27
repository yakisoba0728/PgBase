package query

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

const (
	defaultPageLimit = 50
	maximumPageLimit = 200
)

func CompileInsert(collection schema.Collection, input map[string]json.RawMessage, id uuid.UUID, now time.Time) (Statement, error) {
	decoded, err := decodeInput(collection, input)
	if err != nil {
		return Statement{}, err
	}
	fields := fieldsByOrdinal(collection)
	for _, field := range fields {
		if field.Required && len(field.Default) == 0 {
			if _, present := decoded[field.Name]; !present {
				return Statement{}, validation(field.Name, "required")
			}
		}
	}

	columns := []string{quoted("id"), quoted("created_at"), quoted("updated_at")}
	args := []any{id, now.UTC(), now.UTC()}
	for _, field := range fields {
		value, present := decoded[field.Name]
		if !present {
			continue
		}
		columns = append(columns, quoted(field.ColumnName))
		args = append(args, value)
	}
	placeholders := make([]string, len(args))
	for index := range args {
		placeholders[index] = fmt.Sprintf("$%d", index+1)
	}
	return Statement{
		SQL: "INSERT INTO " + quotedTable(collection) + " (" + strings.Join(columns, ", ") + ") VALUES (" +
			strings.Join(placeholders, ", ") + ") RETURNING " + selectedColumns(collection),
		Args: args,
	}, nil
}

func CompileSelectByID(collection schema.Collection, id uuid.UUID) Statement {
	return Statement{
		SQL:  "SELECT " + selectedColumns(collection) + " FROM " + quotedTable(collection) + " WHERE " + quoted("id") + " = $1",
		Args: []any{id},
	}
}

func CompileUpdate(collection schema.Collection, id uuid.UUID, input map[string]json.RawMessage, now time.Time) (Statement, error) {
	if len(input) == 0 {
		return Statement{}, validation("", "empty_patch")
	}
	decoded, err := decodeInput(collection, input)
	if err != nil {
		return Statement{}, err
	}
	assignments := make([]string, 0, len(decoded)+1)
	args := make([]any, 0, len(decoded)+2)
	for _, field := range fieldsByOrdinal(collection) {
		value, present := decoded[field.Name]
		if !present {
			continue
		}
		args = append(args, value)
		assignments = append(assignments, fmt.Sprintf("%s = $%d", quoted(field.ColumnName), len(args)))
	}
	args = append(args, now.UTC())
	assignments = append(assignments, fmt.Sprintf("%s = $%d", quoted("updated_at"), len(args)))
	args = append(args, id)
	return Statement{
		SQL: "UPDATE " + quotedTable(collection) + " SET " + strings.Join(assignments, ", ") +
			fmt.Sprintf(" WHERE %s = $%d RETURNING %s", quoted("id"), len(args), selectedColumns(collection)),
		Args: args,
	}, nil
}

func CompileDelete(collection schema.Collection, id uuid.UUID) Statement {
	return Statement{
		SQL:  "DELETE FROM " + quotedTable(collection) + " WHERE " + quoted("id") + " = $1",
		Args: []any{id},
	}
}

func CompileList(collection schema.Collection, after *PageCursor, limit int) Statement {
	prefix := "SELECT " + selectedColumns(collection) + " FROM " + quotedTable(collection)
	order := " ORDER BY " + quoted("created_at") + " ASC, " + quoted("id") + " ASC"
	fetchLimit := normalizedFetchLimit(limit)
	if after == nil {
		return Statement{SQL: prefix + order + " LIMIT $1", Args: []any{fetchLimit}}
	}
	return Statement{
		SQL:  prefix + " WHERE (" + quoted("created_at") + ", " + quoted("id") + ") > ($1, $2)" + order + " LIMIT $3",
		Args: []any{after.CreatedAt, after.ID, fetchLimit},
	}
}

func normalizedFetchLimit(limit int) int {
	if limit <= 0 {
		limit = defaultPageLimit
	} else if limit > maximumPageLimit {
		limit = maximumPageLimit
	}
	return limit + 1
}

func selectedColumns(collection schema.Collection) string {
	columns := []string{quoted("id"), quoted("created_at"), quoted("updated_at")}
	for _, field := range fieldsByOrdinal(collection) {
		columns = append(columns, quoted(field.ColumnName))
	}
	return strings.Join(columns, ", ")
}

func quotedTable(collection schema.Collection) string {
	return pgx.Identifier{"app", collection.TableName}.Sanitize()
}

func quoted(name string) string {
	return pgx.Identifier{name}.Sanitize()
}
