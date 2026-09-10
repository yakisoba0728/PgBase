package pgbase

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

const maximumExtensionDatabaseRows = 100

type extensionDatabaseStatement struct {
	SQL   string            `json:"sql"`
	Args  []json.RawMessage `json:"args,omitempty"`
	Query bool              `json:"query,omitempty"`
}

type extensionDatabaseRequest struct {
	Statements []extensionDatabaseStatement `json:"statements"`
}

func (host *extensionHost) callDatabase(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if host.bridge.database == nil {
		return nil, extensionError("database_unavailable", errors.New("trusted database access is disabled"))
	}
	var request extensionDatabaseRequest
	if err := decodeExtensionInput(raw, &request); err != nil || len(request.Statements) == 0 || len(request.Statements) > 32 {
		return nil, extensionError("invalid_database_request", err)
	}
	results := make([]any, 0, len(request.Statements))
	err := host.bridge.database.RunInTransaction(ctx, func(txCtx context.Context) error {
		for _, statement := range request.Statements {
			if strings.TrimSpace(statement.SQL) == "" {
				return errors.New("SQL is required")
			}
			arguments := make([]any, len(statement.Args))
			for i := range statement.Args {
				var value any
				decoder := json.NewDecoder(strings.NewReader(string(statement.Args[i])))
				decoder.UseNumber()
				if err := decoder.Decode(&value); err != nil {
					return err
				}
				arguments[i] = value
			}
			if !statement.Query {
				tag, err := host.bridge.database.Exec(txCtx, statement.SQL, arguments...)
				if err != nil {
					return err
				}
				results = append(results, map[string]any{"rowsAffected": tag.RowsAffected()})
				continue
			}
			rows, err := host.bridge.database.Query(txCtx, statement.SQL, arguments...)
			if err != nil {
				return err
			}
			fields := rows.FieldDescriptions()
			if len(fields) > 64 {
				rows.Close()
				return errors.New("too many result columns")
			}
			items := make([]map[string]any, 0)
			for rows.Next() {
				if len(items) >= maximumExtensionDatabaseRows {
					rows.Close()
					return errors.New("too many result rows")
				}
				values, err := rows.Values()
				if err != nil {
					rows.Close()
					return err
				}
				item := make(map[string]any, len(values))
				for i, value := range values {
					item[string(fields[i].Name)] = value
				}
				items = append(items, item)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			results = append(results, map[string]any{"items": items})
		}
		return nil
	})
	if err != nil {
		return nil, extensionError("database_transaction_failed", err)
	}
	return json.Marshal(map[string]any{"results": results})
}
