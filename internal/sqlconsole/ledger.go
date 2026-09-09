package sqlconsole

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
)

const (
	MaximumHistoryEntries = 50
	coordinatorLockKey    = int64(0x7067626173650008)
	executionGateKey      = int64(0x7067626173651008)
	HistoryRetention      = 30 * 24 * time.Hour
	// Compact write identities and receipts are retained indefinitely. Their
	// unbounded key count is the cost of never making an accepted mutation
	// eligible for automatic replay after its heavier execution row expires.
	DurableWriteReceiptRetention = "indefinite"
)

var ErrCoordinatorBusy = errors.New("another SQL console coordinator is active")

type LedgerIdentity struct {
	AdministratorID  uuid.UUID
	Request          Request
	RequestHash      [32]byte
	QueryFingerprint [32]byte
	IdempotencyHash  [32]byte
}

type Cursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

type Page struct {
	Executions []Execution
	Next       *Cursor
}

type Ledger struct {
	pool *database.Pool
}

type Coordinator struct {
	connection *pgx.Conn
	pool       *database.Pool
	pid        uint32
	closeOnce  sync.Once
}

func NewLedger(pool *database.Pool) (*Ledger, error) {
	if pool == nil {
		return nil, errors.New("SQL console ledger pool is required")
	}
	return &Ledger{pool: pool}, nil
}

func (ledger *Ledger) AcquireCoordinator(ctx context.Context) (*Coordinator, error) {
	connection, err := ledger.pool.OpenCoordinationSession(ctx)
	if err != nil {
		return nil, err
	}
	var acquired bool
	if err := connection.QueryRow(ctx, `select pg_catalog.pg_try_advisory_lock($1)`, coordinatorLockKey).Scan(&acquired); err != nil {
		_ = connection.Close(ctx)
		return nil, err
	}
	if !acquired {
		_ = connection.Close(ctx)
		return nil, ErrCoordinatorBusy
	}
	// A previous coordinator may have lost its session while its independent
	// execution transactions still run. Drain them before reconciling history.
	if _, err := connection.Exec(ctx, `select pg_catalog.pg_advisory_lock($1)`, executionGateKey); err != nil {
		_ = connection.Close(context.Background())
		return nil, err
	}
	return &Coordinator{connection: connection, pool: ledger.pool, pid: connection.PgConn().PID()}, nil
}

func (coordinator *Coordinator) Close() {
	if coordinator == nil {
		return
	}
	coordinator.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = coordinator.connection.Close(ctx)
	})
}

func (coordinator *Coordinator) finishStartup(ctx context.Context) error {
	_, err := coordinator.connection.Exec(ctx, `select pg_catalog.pg_advisory_unlock($1)`, executionGateKey)
	return err
}

const coordinatorOwnedSQL = `select exists(select 1 from pg_catalog.pg_locks
	where locktype='advisory' and pid=$1 and classid=$2 and objid=$3 and objsubid=1 and granted)`

func (coordinator *Coordinator) verify(ctx context.Context) error {
	var owned bool
	err := coordinator.pool.QueryRow(ctx, coordinatorOwnedSQL, coordinator.pid,
		uint32(uint64(coordinatorLockKey)>>32), uint32(uint64(coordinatorLockKey)&0xffffffff)).Scan(&owned)
	if err != nil || !owned {
		return fmt.Errorf("%w: SQL console coordinator ownership lost", ErrClosed)
	}
	return nil
}

func fenceExecution(ctx context.Context, transaction pgx.Tx, coordinator *Coordinator) error {
	if coordinator == nil {
		return nil
	}
	if _, err := transaction.Exec(ctx, `select pg_catalog.pg_advisory_xact_lock_shared($1)`, executionGateKey); err != nil {
		return err
	}
	var owned bool
	err := transaction.QueryRow(ctx, coordinatorOwnedSQL, coordinator.pid,
		uint32(uint64(coordinatorLockKey)>>32), uint32(uint64(coordinatorLockKey)&0xffffffff)).Scan(&owned)
	if err != nil {
		return err
	}
	if !owned {
		return fmt.Errorf("%w: SQL console coordinator ownership lost", ErrClosed)
	}
	return nil
}

func (ledger *Ledger) CreatePending(ctx context.Context, identity LedgerIdentity) (Execution, bool, error) {
	if identity.AdministratorID == uuid.Nil || identity.Request.ExecutionID == uuid.Nil {
		return Execution{}, false, ErrInvalidRequest
	}
	var idempotency any
	connection, err := ledger.pool.Acquire(ctx)
	if err != nil {
		return Execution{}, false, err
	}
	defer connection.Release()
	transaction, err := connection.Begin(ctx)
	if err != nil {
		return Execution{}, false, err
	}
	defer func() { _ = transaction.Rollback(context.Background()) }()
	if identity.Request.Mode == ModeWrite {
		idempotency = identity.IdempotencyHash[:]
		tag, err := transaction.Exec(ctx, `insert into pgbase_meta.sql_console_write_identities
			(execution_id,administrator_id,idempotency_hash,request_hash,dry_run) values ($1,$2,$3,$4,$5)
			on conflict do nothing`, identity.Request.ExecutionID, identity.AdministratorID,
			idempotency, identity.RequestHash[:], identity.Request.DryRun)
		if err != nil {
			return Execution{}, false, err
		}
		if tag.RowsAffected() == 0 {
			_ = transaction.Rollback(ctx)
			return ledger.retainedWrite(ctx, identity)
		}
	}
	row := transaction.QueryRow(ctx, `insert into pgbase_meta.sql_console_executions
		(execution_id,administrator_id,mode,dry_run,idempotency_hash,request_hash,query_fingerprint,status,
		statement_count,created_at,updated_at)
		values($1,$2,$3,$4,$5,$6,$7,'pending',$8,current_timestamp,current_timestamp)
		on conflict do nothing
		returning execution_id,mode,dry_run,status,created_at,started_at,finished_at,result_expires_at`,
		identity.Request.ExecutionID, identity.AdministratorID, identity.Request.Mode, identity.Request.DryRun,
		idempotency, identity.RequestHash[:], identity.QueryFingerprint[:], len(identity.Request.Statements))
	execution, err := scanExecution(row)
	if err == nil {
		if err := transaction.Commit(ctx); err != nil {
			return Execution{}, false, err
		}
		return execution, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Execution{}, false, err
	}
	_ = transaction.Rollback(ctx)
	return ledger.existing(ctx, identity)
}

func (ledger *Ledger) receipt(ctx context.Context, identity LedgerIdentity) (Execution, bool, error) {
	row := ledger.pool.QueryRow(ctx, `select execution_id,committed_at,row_count,affected_rows,result_bytes,
		administrator_id=$2 and request_hash=$3 as same_request
		from pgbase_meta.sql_console_write_receipts
		where execution_id=$1 or (administrator_id=$2 and idempotency_hash=$4)
		order by (execution_id=$1) desc limit 1`, identity.Request.ExecutionID, identity.AdministratorID,
		identity.RequestHash[:], identity.IdempotencyHash[:])
	var execution Execution
	var committedAt time.Time
	var rows, resultBytes int
	var affectedRows int64
	var sameRequest bool
	if err := row.Scan(&execution.ID, &committedAt, &rows, &affectedRows, &resultBytes, &sameRequest); errors.Is(err, pgx.ErrNoRows) {
		return Execution{}, false, nil
	} else if err != nil {
		return Execution{}, false, err
	}
	if !sameRequest {
		return Execution{}, true, ErrConflict
	}
	committed := true
	execution.Mode = ModeWrite
	execution.Status = StatusSucceeded
	execution.CreatedAt = committedAt
	execution.StartedAt = committedAt
	execution.FinishedAt = committedAt
	execution.ResultUntil = committedAt.Add(ResultTTL)
	execution.Result = &Result{Rows: rows, AffectedRows: affectedRows, Bytes: resultBytes, Committed: &committed}
	return execution, true, nil
}

func (ledger *Ledger) existing(ctx context.Context, identity LedgerIdentity) (Execution, bool, error) {
	var idempotency any
	if identity.Request.Mode == ModeWrite {
		idempotency = identity.IdempotencyHash[:]
	}
	row := ledger.pool.QueryRow(ctx, `select execution_id,mode,dry_run,status,created_at,started_at,finished_at,result_expires_at,
		administrator_id=$2 and request_hash=$3 and mode=$4 and dry_run=$5 as same_request
		from pgbase_meta.sql_console_executions
		where execution_id=$1 or ($6::bytea is not null and administrator_id=$2 and idempotency_hash=$6)
		order by (execution_id=$1) desc limit 1`,
		identity.Request.ExecutionID, identity.AdministratorID, identity.RequestHash[:], identity.Request.Mode,
		identity.Request.DryRun, idempotency)
	var execution Execution
	var startedAt, finishedAt, resultUntil *time.Time
	var sameRequest bool
	err := row.Scan(&execution.ID, &execution.Mode, &execution.DryRun, &execution.Status, &execution.CreatedAt,
		&startedAt, &finishedAt, &resultUntil, &sameRequest)
	if errors.Is(err, pgx.ErrNoRows) {
		return Execution{}, false, ErrConflict
	}
	if err != nil {
		return Execution{}, false, err
	}
	setExecutionTimes(&execution, startedAt, finishedAt, resultUntil)
	if !sameRequest {
		return Execution{}, false, ErrConflict
	}
	execution, err = ledger.Get(ctx, identity.AdministratorID, execution.ID)
	return execution, false, err
}

func (ledger *Ledger) MarkRunning(ctx context.Context, actorID, executionID uuid.UUID) (Execution, error) {
	row := ledger.pool.QueryRow(ctx, `update pgbase_meta.sql_console_executions
		set status='running',started_at=current_timestamp,updated_at=current_timestamp
		where execution_id=$1 and administrator_id=$2 and status='pending'
		returning execution_id,mode,dry_run,status,created_at,started_at,finished_at,result_expires_at`, executionID, actorID)
	return scanExecution(row)
}

func (ledger *Ledger) Finish(ctx context.Context, actorID uuid.UUID, execution Execution) error {
	if !terminalStatus(execution.Status) {
		return fmt.Errorf("finish SQL console execution: non-terminal status %q", execution.Status)
	}
	rows, affected, resultBytes := 0, int64(0), 0
	if execution.Result != nil {
		rows = execution.Result.Rows
		affected = execution.Result.AffectedRows
		resultBytes = execution.Result.Bytes
	}
	var sqlState, failureCode any
	if execution.Failure != nil {
		if execution.Failure.SQLState != "" {
			sqlState = execution.Failure.SQLState
		}
		failureCode = execution.Failure.Code
	}
	var committed any
	if execution.Result != nil && execution.Result.Committed != nil {
		committed = *execution.Result.Committed
	} else if execution.Status == StatusFailed || execution.Status == StatusCanceled || execution.Status == StatusTimedOut {
		committed = false
	}
	tag, err := ledger.pool.Exec(ctx, `update pgbase_meta.sql_console_executions set
		status=$3,row_count=$4,affected_rows=$5,result_bytes=$6,sql_state=$7,failure_code=$8,committed=$9,
		updated_at=current_timestamp,finished_at=current_timestamp,result_expires_at=current_timestamp+$10::interval
		where execution_id=$1 and administrator_id=$2 and status in ('pending','running','unknown')`,
		execution.ID, actorID, execution.Status, rows, affected, resultBytes, sqlState, failureCode, committed,
		fmt.Sprintf("%d milliseconds", ResultTTL.Milliseconds()))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return ledger.Cleanup(ctx)
}

func (ledger *Ledger) Cleanup(ctx context.Context) error {
	connection, err := ledger.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer connection.Release()
	transaction, err := connection.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback(context.Background()) }()
	if err := cleanupExecutions(ctx, transaction); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (ledger *Ledger) Get(ctx context.Context, actorID, executionID uuid.UUID) (Execution, error) {
	row := ledger.pool.QueryRow(ctx, `select execution_id,mode,dry_run,status,created_at,started_at,finished_at,result_expires_at,
		row_count,affected_rows,result_bytes,sql_state,failure_code,committed
		from pgbase_meta.sql_console_executions where execution_id=$1 and administrator_id=$2`, executionID, actorID)
	return scanDurableExecution(row)
}

func (ledger *Ledger) Reconcile(ctx context.Context) error {
	connection, err := ledger.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer connection.Release()
	transaction, err := connection.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback(context.Background()) }()
	interval := fmt.Sprintf("%d milliseconds", ResultTTL.Milliseconds())
	if _, err := transaction.Exec(ctx, `update pgbase_meta.sql_console_executions e set
		status='succeeded',row_count=r.row_count,affected_rows=r.affected_rows,result_bytes=r.result_bytes,
		failure_code=null,sql_state=null,committed=true,started_at=coalesce(e.started_at,e.created_at),
		updated_at=current_timestamp,finished_at=coalesce(e.finished_at,r.committed_at),
		result_expires_at=coalesce(e.result_expires_at,r.committed_at+$1::interval)
		from pgbase_meta.sql_console_write_receipts r
		where e.execution_id=r.execution_id and e.administrator_id=r.administrator_id
		and e.idempotency_hash=r.idempotency_hash and e.request_hash=r.request_hash
		and e.status in ('running','unknown')`, interval); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `update pgbase_meta.sql_console_executions set
		status='failed',failure_code='abandoned',committed=false,
		updated_at=current_timestamp,finished_at=current_timestamp,result_expires_at=current_timestamp+$1::interval
		where status in ('pending','running')`, interval); err != nil {
		return err
	}
	if err := cleanupExecutions(ctx, transaction); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (ledger *Ledger) List(ctx context.Context, actorID uuid.UUID, cursor Cursor) (Page, error) {
	if err := ledger.Cleanup(ctx); err != nil {
		return Page{}, err
	}
	rows, err := ledger.pool.Query(ctx, `select execution_id,mode,dry_run,status,created_at,started_at,finished_at,result_expires_at,
		row_count,affected_rows,result_bytes,sql_state,failure_code,committed
		from pgbase_meta.sql_console_executions
		where administrator_id=$1 and ($2::timestamptz is null or (created_at,execution_id)<($2,$3))
		order by created_at desc,execution_id desc limit $4`, actorID, nullableCursorTime(cursor), nullableCursorID(cursor), MaximumHistoryEntries+1)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	page := Page{Executions: make([]Execution, 0, MaximumHistoryEntries)}
	for rows.Next() {
		execution, err := scanDurableExecution(rows)
		if err != nil {
			return Page{}, err
		}
		page.Executions = append(page.Executions, execution)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	if len(page.Executions) > MaximumHistoryEntries {
		last := page.Executions[MaximumHistoryEntries-1]
		page.Executions = page.Executions[:MaximumHistoryEntries]
		page.Next = &Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

func nullableCursorTime(cursor Cursor) any {
	if cursor.CreatedAt.IsZero() || cursor.ID == uuid.Nil {
		return nil
	}
	return cursor.CreatedAt
}

func nullableCursorID(cursor Cursor) any {
	if cursor.CreatedAt.IsZero() || cursor.ID == uuid.Nil {
		return nil
	}
	return cursor.ID
}

type rowScanner interface {
	Scan(...any) error
}

func scanExecution(row rowScanner) (Execution, error) {
	var execution Execution
	var startedAt, finishedAt, resultUntil *time.Time
	err := row.Scan(&execution.ID, &execution.Mode, &execution.DryRun, &execution.Status, &execution.CreatedAt,
		&startedAt, &finishedAt, &resultUntil)
	setExecutionTimes(&execution, startedAt, finishedAt, resultUntil)
	return execution, err
}

func scanDurableExecution(row rowScanner) (Execution, error) {
	var execution Execution
	var startedAt, finishedAt, resultUntil *time.Time
	var rowCount, resultBytes int
	var affectedRows int64
	var sqlState, failureCode *string
	var committed *bool
	err := row.Scan(&execution.ID, &execution.Mode, &execution.DryRun, &execution.Status, &execution.CreatedAt,
		&startedAt, &finishedAt, &resultUntil, &rowCount, &affectedRows, &resultBytes,
		&sqlState, &failureCode, &committed)
	if err != nil {
		return Execution{}, err
	}
	setExecutionTimes(&execution, startedAt, finishedAt, resultUntil)
	if terminalStatus(execution.Status) {
		execution.Result = &Result{Rows: rowCount, AffectedRows: affectedRows, Bytes: resultBytes, DryRun: execution.DryRun, Committed: committed}
	}
	if failureCode != nil {
		execution.Failure = &Failure{Code: *failureCode, Message: failureMessage(*failureCode)}
		if sqlState != nil {
			execution.Failure.SQLState = *sqlState
		}
	}
	return execution, nil
}

func setExecutionTimes(execution *Execution, startedAt, finishedAt, resultUntil *time.Time) {
	if startedAt != nil {
		execution.StartedAt = *startedAt
	}
	if finishedAt != nil {
		execution.FinishedAt = *finishedAt
	}
	if resultUntil != nil {
		execution.ResultUntil = *resultUntil
	}
}

func terminalStatus(status Status) bool {
	switch status {
	case StatusSucceeded, StatusFailed, StatusCanceled, StatusTimedOut, StatusUnknown:
		return true
	default:
		return false
	}
}

func failureMessage(code string) string {
	switch code {
	case "canceled":
		return "Execution was canceled."
	case "timed_out":
		return "Execution timed out."
	case "commit_unknown":
		return "The write commit outcome could not be confirmed."
	case "rejected":
		return "The statement is outside the allowed SQL console grammar."
	default:
		return "Execution failed."
	}
}
