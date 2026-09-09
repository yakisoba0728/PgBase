package pgbase

import (
	"context"
	"fmt"

	"github.com/webisnoteasy/PgBase/internal/api"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/schema"
	"github.com/webisnoteasy/PgBase/internal/sqlconsole"
)

func newSQLConsole(
	ctx context.Context,
	control *database.Pool,
	catalog schema.Catalog,
	options Options,
) (*sqlconsole.Service, *api.SQLConsoleHandlers, error) {
	configured := options.SQLConsole
	if configured == nil {
		return nil, nil, nil
	}
	pools, err := sqlconsole.OpenPools(ctx, configured.poolOptions(catalog, options.AllowInsecureDatabase))
	if err != nil {
		return nil, nil, fmt.Errorf("initialize SQL console pools: %w", err)
	}
	runner, err := sqlconsole.NewRunner(pools, control)
	if err != nil {
		pools.Close()
		return nil, nil, fmt.Errorf("initialize SQL console runner: %w", err)
	}
	ledger, err := sqlconsole.NewLedger(control)
	if err != nil {
		runner.Close()
		return nil, nil, fmt.Errorf("initialize SQL console ledger: %w", err)
	}
	service, err := sqlconsole.NewServiceWithOptions(ctx, runner, ledger, options.TokenSecret, configured.serviceOptions())
	if err != nil {
		runner.Close()
		return nil, nil, fmt.Errorf("initialize SQL console service: %w", err)
	}
	targets := make([]api.SQLConsoleWriteTarget, len(configured.WriteTargets))
	for index, target := range configured.WriteTargets {
		targets[index] = api.SQLConsoleWriteTarget{
			Schema: target.Schema, Table: target.Table, Columns: append([]string(nil), target.Columns...),
		}
	}
	handlers, err := api.NewSQLConsoleHandlers(service, options.TokenSecret, targets)
	if err != nil {
		_ = service.Close(context.Background())
		return nil, nil, fmt.Errorf("initialize SQL console HTTP handlers: %w", err)
	}
	return service, handlers, nil
}
