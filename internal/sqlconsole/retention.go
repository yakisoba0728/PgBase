package sqlconsole

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func (ledger *Ledger) retainedWrite(ctx context.Context, identity LedgerIdentity) (Execution, bool, error) {
	if receipt, found, err := ledger.receipt(ctx, identity); err != nil || found {
		return receipt, false, err
	}
	execution, _, err := ledger.existing(ctx, identity)
	if err == nil {
		return execution, false, nil
	}
	if !errors.Is(err, ErrConflict) && !errors.Is(err, pgx.ErrNoRows) {
		return Execution{}, false, err
	}
	row := ledger.pool.QueryRow(ctx, `select execution_id,dry_run,status,finished_at,failure_code,
		administrator_id=$2 and request_hash=$3 as same_request
		from pgbase_meta.sql_console_write_identities
		where execution_id=$1 or (administrator_id=$2 and idempotency_hash=$4)
		order by (execution_id=$1) desc limit 1`, identity.Request.ExecutionID, identity.AdministratorID,
		identity.RequestHash[:], identity.IdempotencyHash[:])
	var finishedAt *time.Time
	var failureCode *string
	var sameRequest bool
	if err := row.Scan(&execution.ID, &execution.DryRun, &execution.Status, &finishedAt, &failureCode, &sameRequest); err != nil {
		return Execution{}, false, err
	}
	if !sameRequest {
		return Execution{}, false, ErrConflict
	}
	if finishedAt == nil || !terminalStatus(execution.Status) {
		return Execution{}, false, ErrConflict
	}
	execution.Mode = ModeWrite
	execution.CreatedAt = *finishedAt
	execution.FinishedAt = *finishedAt
	execution.ResultUntil = finishedAt.Add(ResultTTL)
	execution.Result = &Result{DryRun: execution.DryRun}
	if execution.Status != StatusUnknown {
		committed := execution.Status == StatusSucceeded && !execution.DryRun
		execution.Result.Committed = &committed
	}
	if failureCode != nil {
		execution.Failure = &Failure{Code: *failureCode, Message: failureMessage(*failureCode)}
	}
	return execution, false, nil
}

func cleanupExecutions(ctx context.Context, transaction pgx.Tx) error {
	// Lock a bounded batch first. Reservation rows are never deleted, so a
	// concurrent replay cannot become a new execution while history is removed.
	_, err := transaction.Exec(ctx, `with expired as materialized (
		select execution_id from pgbase_meta.sql_console_executions
		where finished_at < current_timestamp-$1::interval
		order by finished_at,execution_id limit 500 for update skip locked
	), retained as (
		update pgbase_meta.sql_console_write_identities i
		set status=e.status,finished_at=e.finished_at,failure_code=e.failure_code
		from pgbase_meta.sql_console_executions e join expired x using(execution_id)
		where i.execution_id=e.execution_id and i.administrator_id=e.administrator_id
		and i.idempotency_hash=e.idempotency_hash and i.request_hash=e.request_hash
		returning i.execution_id
	)
	delete from pgbase_meta.sql_console_executions e using expired x
	where e.execution_id=x.execution_id and (e.mode='read' or e.execution_id in (select execution_id from retained))`,
		fmt.Sprintf("%d seconds", int64(HistoryRetention/time.Second)))
	return err
}
