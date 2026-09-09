package pgbase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/jsvm"
)

type javaScriptMigrationHost struct{ tx pgx.Tx }

type javaScriptMigrationStatement struct {
	SQL  string            `json:"sql"`
	Args []json.RawMessage `json:"args,omitempty"`
}

type javaScriptMigrationSnapshot struct {
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
}

func (a *App) JavaScriptMigrations() ([]UserMigration, error) {
	if a == nil {
		return nil, errors.New("pgbase app is nil")
	}
	if a.javaScriptRuntime == nil {
		return nil, nil
	}
	manifest := a.javaScriptRuntime.bundle.Manifest()
	result := make([]UserMigration, 0, len(manifest.Migrations))
	for _, descriptor := range manifest.Migrations {
		bundle, source, err := a.javaScriptRuntime.bundle.MigrationBundle(descriptor.ID)
		if err != nil {
			return nil, err
		}
		descriptor := descriptor
		migration := UserMigration{ID: descriptor.ID, Source: source, BeforeSnapshot: append([]byte(nil), descriptor.BeforeSnapshot...),
			AfterSnapshot: append([]byte(nil), descriptor.AfterSnapshot...), IrreversibleReason: descriptor.IrreversibleReason}
		migration.Up = javaScriptMigrationCallback(bundle, descriptor, descriptor.Up)
		if descriptor.Down != "" {
			migration.Down = javaScriptMigrationCallback(bundle, descriptor, descriptor.Down)
		} else {
			reason := descriptor.IrreversibleReason
			migration.Down = func(context.Context, pgx.Tx) error { return &IrreversibleMigrationError{Reason: reason} }
		}
		result = append(result, migration)
	}
	return result, nil
}

func javaScriptMigrationCallback(bundle *jsvm.Bundle, descriptor jsvm.Migration, export string) func(context.Context, pgx.Tx) error {
	return func(ctx context.Context, tx pgx.Tx) (resultErr error) {
		limits := jsvm.Limits{Concurrency: 1, Queue: 1, AdmissionTimeout: time.Second, DefaultTimeout: time.Minute,
			MaximumTimeout: 5 * time.Minute, MaximumHostCalls: 4096, MaximumInputBytes: 4 << 20, MaximumOutputBytes: 16 << 20}
		runtime, err := jsvm.NewRuntime(bundle, limits)
		if err != nil {
			return err
		}
		defer func() {
			resultErr = errors.Join(resultErr, runtime.Close(context.WithoutCancel(ctx)))
		}()
		input, err := json.Marshal(map[string]string{"id": descriptor.ID})
		if err != nil {
			return err
		}
		_, err = runtime.Invoke(ctx, jsvm.Invocation{Handler: jsvm.HandlerRef{Module: descriptor.Module, Export: export},
			Capabilities: []string{"migration"}, Input: input, Host: &javaScriptMigrationHost{tx: tx}, Timeout: time.Minute})
		return err
	}
}

func (host *javaScriptMigrationHost) Call(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	if host == nil || host.tx == nil {
		return nil, extensionError("migration_transaction_unavailable", nil)
	}
	switch method {
	case "migration.exec":
		statement, args, err := decodeJavaScriptMigrationStatement(raw)
		if err != nil {
			return nil, err
		}
		tag, err := host.tx.Exec(ctx, statement.SQL, args...)
		if err != nil {
			return nil, extensionError("migration_exec_failed", err)
		}
		return json.Marshal(map[string]int64{"rowsAffected": tag.RowsAffected()})
	case "migration.query":
		statement, args, err := decodeJavaScriptMigrationStatement(raw)
		if err != nil {
			return nil, err
		}
		return host.query(ctx, statement.SQL, args)
	case "migration.applySnapshot":
		var snapshot javaScriptMigrationSnapshot
		if err := decodeStrictJSON(raw, &snapshot); err != nil || len(snapshot.Before) == 0 || len(snapshot.After) == 0 {
			return nil, extensionError("invalid_migration_snapshot", err)
		}
		if err := ApplyUserMigrationSnapshot(ctx, host.tx, snapshot.Before, snapshot.After); err != nil {
			return nil, extensionError("migration_snapshot_failed", err)
		}
		return json.RawMessage(`{"applied":true}`), nil
	default:
		return nil, extensionError("unsupported_migration_operation", nil)
	}
}

func decodeJavaScriptMigrationStatement(raw json.RawMessage) (javaScriptMigrationStatement, []any, error) {
	var statement javaScriptMigrationStatement
	if err := decodeStrictJSON(raw, &statement); err != nil || strings.TrimSpace(statement.SQL) == "" || len(statement.SQL) > 1<<20 || len(statement.Args) > 256 {
		return statement, nil, extensionError("invalid_migration_statement", err)
	}
	args := make([]any, len(statement.Args))
	for index, value := range statement.Args {
		decoder := json.NewDecoder(bytes.NewReader(value))
		decoder.UseNumber()
		var decoded any
		if err := decoder.Decode(&decoded); err != nil {
			return statement, nil, extensionError("invalid_migration_statement", err)
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			return statement, nil, extensionError("invalid_migration_statement", errors.New("argument has trailing JSON"))
		}
		switch typed := decoded.(type) {
		case nil, bool, string:
			args[index] = typed
		case json.Number:
			args[index] = typed.String()
		default:
			encoded, err := json.Marshal(typed)
			if err != nil {
				return statement, nil, extensionError("invalid_migration_statement", err)
			}
			args[index] = string(encoded)
		}
	}
	return statement, args, nil
}

func (host *javaScriptMigrationHost) query(ctx context.Context, sql string, args []any) (json.RawMessage, error) {
	rows, err := host.tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, extensionError("migration_query_failed", err)
	}
	defer rows.Close()
	fields := rows.FieldDescriptions()
	if len(fields) > 64 {
		return nil, extensionError("migration_query_limit", errors.New("too many columns"))
	}
	result := make([]map[string]any, 0)
	for rows.Next() {
		if len(result) >= 1000 {
			return nil, extensionError("migration_query_limit", errors.New("too many rows"))
		}
		values, err := rows.Values()
		if err != nil {
			return nil, extensionError("migration_query_failed", err)
		}
		row := make(map[string]any, len(fields))
		for index, field := range fields {
			row[string(field.Name)] = values[index]
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, extensionError("migration_query_failed", err)
	}
	encoded, err := json.Marshal(map[string]any{"rows": result})
	if err != nil {
		return nil, fmt.Errorf("encode migration query result: %w", err)
	}
	return encoded, nil
}
