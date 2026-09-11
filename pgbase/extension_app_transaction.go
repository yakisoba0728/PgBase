package pgbase

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordops"
)

func (host *extensionHost) callAppTransaction(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	if host.principal.Kind != identity.Administrator || host.bridge.appControl == nil || host.bridge.appControl.app == nil {
		return nil, extensionError("app_control_denied", errors.New("administrator authority is required"))
	}
	host.databaseMu.Lock()
	defer host.databaseMu.Unlock()
	switch method {
	case "app-control.databaseBegin":
		if host.databaseTx != nil {
			return nil, errors.New("database transaction is already active")
		}
		tx, err := host.bridge.appControl.app.pool.Begin(ctx)
		if err != nil {
			return nil, err
		}
		host.databaseTx, host.databaseToken = tx, uuid.New()
		host.recordOutcome = recordops.NewTransactionOutcome()
		host.objectTx = newExtensionObjectTransactionState(tx, host.bridge.objects)
		return json.Marshal(map[string]any{"token": host.databaseToken, "transactional": true})
	case "app-control.databaseExec":
		if host.databaseTx == nil {
			return nil, errors.New("database transaction is not active")
		}
		var request struct {
			Token string
			SQL   string
			Args  []any
			Query bool
		}
		if err := decodeExtensionInput(raw, &request); err != nil {
			return nil, err
		}
		if request.Token != host.databaseToken.String() || strings.TrimSpace(request.SQL) == "" {
			return nil, errors.New("invalid database transaction request")
		}
		if request.Query {
			rows, err := host.databaseTx.Query(ctx, request.SQL, request.Args...)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			fields, items := rows.FieldDescriptions(), []map[string]any{}
			if len(fields) > 64 {
				return nil, errors.New("too many result columns")
			}
			for rows.Next() {
				if len(items) >= maximumExtensionDatabaseRows {
					return nil, errors.New("too many result rows")
				}
				values, err := rows.Values()
				if err != nil {
					return nil, err
				}
				item := map[string]any{}
				for i, value := range values {
					item[string(fields[i].Name)] = value
				}
				items = append(items, item)
			}
			if err := rows.Err(); err != nil {
				return nil, err
			}
			return json.Marshal(map[string]any{"items": items})
		}
		tag, err := host.databaseTx.Exec(ctx, request.SQL, request.Args...)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"rowsAffected": tag.RowsAffected()})
	case "app-control.databaseCommit", "app-control.databaseRollback":
		var request struct{ Token string }
		if err := decodeExtensionInput(raw, &request); err != nil {
			return nil, err
		}
		if host.databaseTx == nil || request.Token != host.databaseToken.String() {
			return nil, errors.New("invalid database transaction token")
		}
		tx := host.databaseTx
		recordOutcome := host.recordOutcome
		objectTx := host.objectTx
		host.databaseTx, host.databaseToken = nil, uuid.Nil
		host.recordOutcome = nil
		host.objectTx = nil
		var err error
		if method == "app-control.databaseCommit" {
			err = tx.Commit(ctx)
			recordOutcome.Finish(ctx, err)
			_ = objectTx.finish(ctx, false)
		} else {
			err = tx.Rollback(ctx)
			if err == nil {
				recordOutcome.Finish(ctx, recordops.ErrTransactionRolledBack)
				err = objectTx.finish(ctx, true)
			} else {
				recordOutcome.Finish(ctx, err)
				_ = objectTx.finish(ctx, false)
			}
		}
		if err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			return nil, err
		}
		return json.Marshal(map[string]bool{"committed": method == "app-control.databaseCommit", "rolledBack": method == "app-control.databaseRollback"})
	}
	return nil, errors.New("unsupported database transaction operation")
}

func (host *extensionHost) rollbackAppTransaction() error {
	host.databaseMu.Lock()
	defer host.databaseMu.Unlock()
	if host.databaseTx == nil {
		return nil
	}
	tx := host.databaseTx
	recordOutcome := host.recordOutcome
	objectTx := host.objectTx
	host.databaseTx, host.databaseToken = nil, uuid.Nil
	host.recordOutcome = nil
	host.objectTx = nil
	ctx := host.authorityContext
	if ctx == nil {
		ctx = context.Background()
	}
	err := tx.Rollback(context.WithoutCancel(ctx))
	if err == nil {
		recordOutcome.Finish(ctx, recordops.ErrTransactionRolledBack)
		return objectTx.finish(ctx, true)
	}
	recordOutcome.Finish(ctx, err)
	_ = objectTx.finish(ctx, false)
	return err
}
