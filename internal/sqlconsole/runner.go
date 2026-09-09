package sqlconsole

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/webisnoteasy/PgBase/internal/database"
)

var (
	ErrResultLimit     = errors.New("SQL console result limit exceeded")
	ErrAffectedLimit   = errors.New("SQL console affected-row limit exceeded")
	ErrTargetChanged   = errors.New("SQL console write target changed")
	ErrCommitUnknown   = errors.New("SQL console commit outcome is unknown")
	ErrReceiptMismatch = errors.New("SQL console commit receipt does not match")
)

type RunInput struct {
	AdministratorID uuid.UUID
	Request         Request
	RequestHash     [32]byte
	IdempotencyHash [32]byte
}

type StatementExecutionError struct {
	Index int
	Err   error
}

func (failure *StatementExecutionError) Error() string {
	return fmt.Sprintf("execute SQL console statement %d: %v", failure.Index, failure.Err)
}

func (failure *StatementExecutionError) Unwrap() error { return failure.Err }

type Runner struct {
	pools   *Pools
	control *database.Pool
	commit  func(context.Context, pgx.Tx) error
}

func NewRunner(pools *Pools, control *database.Pool) (*Runner, error) {
	if pools == nil || control == nil {
		return nil, errors.New("SQL console runner requires restricted and control pools")
	}
	return &Runner{pools: pools, control: control, commit: func(ctx context.Context, tx pgx.Tx) error {
		return tx.Commit(ctx)
	}}, nil
}

func (runner *Runner) Validate(request Request) error {
	plan, err := Analyze(request)
	if err != nil {
		return err
	}
	return runner.pools.ValidatePlan(plan)
}

func (runner *Runner) Close() { runner.pools.Close() }

func (runner *Runner) Run(ctx context.Context, input RunInput) (Result, error) {
	started := time.Now()
	if ctx == nil || input.AdministratorID == uuid.Nil || input.Request.ExecutionID == uuid.Nil {
		return Result{}, fmt.Errorf("%w: runner identity is incomplete", ErrInvalidRequest)
	}
	request, err := NormalizeRequest(input.Request, input.Request.Mode == ModeWrite)
	if err != nil {
		return Result{}, err
	}
	plan, err := Analyze(request)
	if err != nil {
		return Result{}, err
	}
	if err := runner.pools.ValidatePlan(plan); err != nil {
		return Result{}, err
	}

	runContext, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()
	connection, err := runner.acquire(runContext, request.Mode)
	if err != nil {
		return Result{}, err
	}
	borrowed := &borrowedConnection{connection: connection}
	defer borrowed.release()
	var cancellationObserved atomic.Bool

	options := pgx.TxOptions{AccessMode: pgx.ReadOnly}
	if request.Mode == ModeWrite {
		options.AccessMode = pgx.ReadWrite
	}
	tx, err := connection.BeginTx(runContext, options)
	if err != nil {
		if cancellationObserved.Load() || runContext.Err() != nil {
			borrowed.discard()
		}
		return Result{}, err
	}
	transactionOpen := true
	defer func() {
		if transactionOpen {
			rollbackContext, rollbackCancel := context.WithTimeout(context.Background(), time.Second)
			_ = tx.Rollback(rollbackContext)
			rollbackCancel()
		}
		if cancellationObserved.Load() || runContext.Err() != nil {
			borrowed.discard()
		}
	}()
	cancelWatcherDone := make(chan struct{})
	cancelWatcherExited := make(chan struct{})
	go func() {
		defer close(cancelWatcherExited)
		watchCancellation(runContext, connection, cancelWatcherDone, &cancellationObserved)
	}()
	defer func() {
		close(cancelWatcherDone)
		<-cancelWatcherExited
	}()

	if _, err := tx.Exec(runContext, `select pg_catalog.set_config('statement_timeout',$1,true),
		pg_catalog.set_config('lock_timeout',$2,true)`, postgresMilliseconds(request.Timeout), postgresMilliseconds(LockTimeout)); err != nil {
		return Result{}, err
	}
	if request.Mode == ModeWrite {
		if err := runner.lockAndRevalidate(runContext, tx, plan); err != nil {
			return Result{}, err
		}
	}

	result := Result{Statements: make([]StatementResult, 0, len(request.Statements)), DryRun: request.DryRun}
	for index, statement := range request.Statements {
		statementResult, err := executeStatement(runContext, tx, statement, plan.Statements[index].Kind, &result)
		if err != nil {
			return Result{}, &StatementExecutionError{Index: index, Err: err}
		}
		result.Statements = append(result.Statements, statementResult)
	}
	result.Elapsed = time.Since(started)

	if request.Mode == ModeRead || request.DryRun {
		if err := tx.Rollback(runContext); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			return Result{}, err
		}
		transactionOpen = false
		committed := false
		result.Committed = &committed
		return result, nil
	}

	if _, err := tx.Exec(runContext, `insert into pgbase_meta.sql_console_write_receipts
		(execution_id,administrator_id,idempotency_hash,request_hash,row_count,affected_rows,result_bytes,committed_at)
		values($1,$2,$3,$4,$5,$6,$7,current_timestamp)`,
		request.ExecutionID, input.AdministratorID, input.IdempotencyHash[:], input.RequestHash[:],
		result.Rows, result.AffectedRows, result.Bytes); err != nil {
		return Result{}, fmt.Errorf("write SQL console receipt: %w", err)
	}
	commitErr := runner.commit(runContext, tx)
	transactionOpen = false
	if commitErr == nil {
		committed := true
		result.Committed = &committed
		return result, nil
	}
	borrowed.discard()
	committed, reconcileErr := runner.reconcileReceipt(context.Background(), input, result)
	if reconcileErr != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrCommitUnknown, reconcileErr)
	}
	if !committed {
		return Result{}, fmt.Errorf("commit SQL console transaction: %w", commitErr)
	}
	confirmed := true
	result.Committed = &confirmed
	return result, nil
}

func (runner *Runner) acquire(ctx context.Context, mode Mode) (*pgxpool.Conn, error) {
	if mode == ModeWrite {
		return runner.pools.acquireWriter(ctx)
	}
	return runner.pools.acquireRead(ctx)
}

type borrowedConnection struct {
	connection *pgxpool.Conn
	discarded  bool
}

func (borrowed *borrowedConnection) release() {
	if borrowed != nil && borrowed.connection != nil && !borrowed.discarded {
		borrowed.connection.Release()
	}
}

func (borrowed *borrowedConnection) discard() {
	if borrowed == nil || borrowed.connection == nil || borrowed.discarded {
		return
	}
	borrowed.discarded = true
	connection := borrowed.connection.Hijack()
	closeContext, cancel := context.WithTimeout(context.Background(), time.Second)
	_ = connection.Close(closeContext)
	cancel()
}

func watchCancellation(ctx context.Context, connection *pgxpool.Conn, done <-chan struct{}, observed *atomic.Bool) {
	select {
	case <-ctx.Done():
		observed.Store(true)
		cancelContext, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = connection.Conn().PgConn().CancelRequest(cancelContext)
		cancel()
	case <-done:
	}
}

func postgresMilliseconds(value time.Duration) string {
	milliseconds := value / time.Millisecond
	if value%time.Millisecond != 0 {
		milliseconds++
	}
	return strconv.FormatInt(int64(milliseconds), 10)
}

func (runner *Runner) lockAndRevalidate(ctx context.Context, tx pgx.Tx, plan Plan) error {
	used := make(map[RelationRef]targetSnapshot)
	for _, statement := range plan.Statements {
		if statement.WriteTarget == nil {
			continue
		}
		snapshot, ok := runner.pools.targets[*statement.WriteTarget]
		if !ok {
			return ErrUnsafeWriteTarget
		}
		used[snapshot.Reference] = snapshot
	}
	for _, snapshot := range sortedTargetSnapshots(used) {
		identifier := pgx.Identifier{snapshot.Reference.Schema, snapshot.Reference.Name}.Sanitize()
		if _, err := tx.Exec(ctx, "lock table "+identifier+" in row exclusive mode"); err != nil {
			return fmt.Errorf("lock SQL console target: %w", err)
		}
		if err := revalidateTarget(ctx, tx, snapshot, runner.pools.targets); err != nil {
			return err
		}
	}
	return nil
}

func revalidateTarget(ctx context.Context, tx pgx.Tx, expected targetSnapshot, allowed map[RelationRef]targetSnapshot) error {
	var oid uint32
	var kind, persistence string
	var partitioned, rowSecurity, forceRowSecurity, inherited, userTrigger, userRule, policy bool
	err := tx.QueryRow(ctx, `select c.oid,c.relkind::text,c.relpersistence::text,c.relispartition,
		c.relrowsecurity,c.relforcerowsecurity,
		exists(select 1 from pg_catalog.pg_inherits where inhrelid=c.oid or inhparent=c.oid),
		exists(select 1 from pg_catalog.pg_trigger where tgrelid=c.oid and not tgisinternal),
		exists(select 1 from pg_catalog.pg_rewrite where ev_class=c.oid and rulename <> '_RETURN'),
		exists(select 1 from pg_catalog.pg_policy where polrelid=c.oid)
		from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
		where n.nspname=$1 and c.relname=$2`, expected.Reference.Schema, expected.Reference.Name).Scan(
		&oid, &kind, &persistence, &partitioned, &rowSecurity, &forceRowSecurity,
		&inherited, &userTrigger, &userRule, &policy,
	)
	if err != nil || oid != expected.OID || kind != "r" || persistence != "p" || partitioned || rowSecurity ||
		forceRowSecurity || inherited || userTrigger || userRule || policy {
		return fmt.Errorf("%w: %s.%s", ErrTargetChanged, expected.Reference.Schema, expected.Reference.Name)
	}
	rows, err := tx.Query(ctx, `select a.attname,t.oid,n.nspname,t.typname,a.atthasdef,
		a.attgenerated::text,a.attidentity::text from pg_catalog.pg_attribute a
		join pg_catalog.pg_type t on t.oid=a.atttypid join pg_catalog.pg_namespace n on n.oid=t.typnamespace
		where a.attrelid=$1 and a.attnum>0 and not a.attisdropped order by a.attnum`, oid)
	if err != nil {
		return err
	}
	actualColumns := make(map[string]uint32)
	for rows.Next() {
		var name, typeSchema, typeName, generated, identity string
		var typeOID uint32
		var hasDefault bool
		if err := rows.Scan(&name, &typeOID, &typeSchema, &typeName, &hasDefault, &generated, &identity); err != nil {
			rows.Close()
			return err
		}
		if typeSchema != "pg_catalog" || !safeType(typeName) || hasDefault || generated != "" || identity != "" {
			rows.Close()
			return fmt.Errorf("%w: unsafe column %s", ErrTargetChanged, name)
		}
		actualColumns[name] = typeOID
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if !sameColumnOIDs(actualColumns, expected.Columns) {
		return fmt.Errorf("%w: columns on %s.%s", ErrTargetChanged, expected.Reference.Schema, expected.Reference.Name)
	}
	allowedOIDs := make(map[uint32]struct{}, len(allowed))
	for _, target := range allowed {
		allowedOIDs[target.OID] = struct{}{}
	}
	constraints, err := tx.Query(ctx, `select contype::text,conrelid,confrelid,confupdtype::text,confdeltype::text,convalidated
		from pg_catalog.pg_constraint where conrelid=$1 or confrelid=$1`, oid)
	if err != nil {
		return err
	}
	defer constraints.Close()
	for constraints.Next() {
		var kind, updateAction, deleteAction string
		var relationOID, foreignOID uint32
		var validated bool
		if err := constraints.Scan(&kind, &relationOID, &foreignOID, &updateAction, &deleteAction, &validated); err != nil {
			return err
		}
		if !validated {
			return ErrTargetChanged
		}
		switch kind {
		case "p", "u", "n":
		case "f":
			_, relationAllowed := allowedOIDs[relationOID]
			_, foreignAllowed := allowedOIDs[foreignOID]
			if !relationAllowed || !foreignAllowed || !safeForeignKeyAction(updateAction) || !safeForeignKeyAction(deleteAction) {
				return ErrTargetChanged
			}
		default:
			return ErrTargetChanged
		}
	}
	return constraints.Err()
}

func sameColumnOIDs(left, right map[string]uint32) bool {
	if len(left) != len(right) {
		return false
	}
	for name, oid := range left {
		if right[name] != oid {
			return false
		}
	}
	return true
}

func executeStatement(ctx context.Context, tx pgx.Tx, statement Statement, kind StatementKind, total *Result) (StatementResult, error) {
	parameters := make([]any, len(statement.Parameters))
	for index, parameter := range statement.Parameters {
		value, err := parameterValue(parameter)
		if err != nil {
			return StatementResult{}, err
		}
		parameters[index] = value
	}
	arguments := []any{pgx.QueryExecModeDescribeExec, pgx.QueryResultFormats{pgx.TextFormatCode}}
	arguments = append(arguments, parameters...)
	rows, err := tx.Query(ctx, statement.SQL, arguments...)
	if err != nil {
		return StatementResult{}, err
	}
	fields := rows.FieldDescriptions()
	if len(fields) > MaximumResultColumns {
		rows.Close()
		return StatementResult{}, fmt.Errorf("%w: columns", ErrResultLimit)
	}
	result := StatementResult{Columns: make([]Column, len(fields))}
	typeMap := tx.Conn().TypeMap()
	for index, field := range fields {
		typeValue, ok := typeMap.TypeForOID(field.DataTypeOID)
		if !ok {
			rows.Close()
			return StatementResult{}, fmt.Errorf("unsupported PostgreSQL result type OID %d", field.DataTypeOID)
		}
		result.Columns[index] = Column{Name: field.Name, TypeOID: field.DataTypeOID, TypeName: typeValue.Name}
	}
	for rows.Next() {
		if total.Rows >= MaximumResultRows {
			rows.Close()
			return StatementResult{}, fmt.Errorf("%w: rows", ErrResultLimit)
		}
		rawValues := rows.RawValues()
		row := make([]Cell, len(rawValues))
		for index, raw := range rawValues {
			cell, err := resultCell(typeMap, fields[index].DataTypeOID, raw)
			if err != nil {
				rows.Close()
				return StatementResult{}, err
			}
			row[index] = cell
		}
		encoded, err := EncodeCells(row)
		if err != nil {
			rows.Close()
			return StatementResult{}, err
		}
		if total.Bytes+len(encoded) > MaximumResultBytes {
			rows.Close()
			return StatementResult{}, fmt.Errorf("%w: bytes", ErrResultLimit)
		}
		total.Bytes += len(encoded)
		total.Rows++
		result.Rows = append(result.Rows, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return StatementResult{}, err
	}
	tag := rows.CommandTag()
	rows.Close()
	result.Command = tag.String()
	if kind == StatementInsert || kind == StatementUpdate || kind == StatementDelete {
		result.AffectedRows = tag.RowsAffected()
	}
	if result.AffectedRows > 0 {
		if total.AffectedRows > MaximumAffectedRows-result.AffectedRows {
			return StatementResult{}, ErrAffectedLimit
		}
		total.AffectedRows += result.AffectedRows
	}
	return result, nil
}

func parameterValue(parameter Parameter) (any, error) {
	switch parameter.Kind {
	case ParameterNull:
		return nil, nil
	case ParameterString:
		return parameter.Text, nil
	case ParameterBoolean:
		return parameter.Text == "true", nil
	case ParameterInteger, ParameterDecimal:
		var number pgtype.Numeric
		if err := number.Scan(parameter.Text); err != nil {
			return nil, err
		}
		return number, nil
	case ParameterJSON:
		return json.RawMessage(parameter.Text), nil
	case ParameterBytes:
		return base64.StdEncoding.Strict().DecodeString(parameter.Text)
	case ParameterUUID:
		return uuid.Parse(parameter.Text)
	case ParameterTimestamp:
		return time.Parse(time.RFC3339Nano, parameter.Text)
	default:
		return nil, ErrInvalidRequest
	}
}

func resultCell(typeMap *pgtype.Map, oid uint32, raw []byte) (Cell, error) {
	if raw == nil {
		return Cell{Kind: CellNull}, nil
	}
	text := string(raw)
	switch oid {
	case pgtype.BoolOID:
		if text == "t" {
			text = "true"
		} else if text == "f" {
			text = "false"
		}
		return Cell{Kind: CellBoolean, Text: text}, nil
	case pgtype.Int2OID, pgtype.Int4OID, pgtype.Int8OID:
		return Cell{Kind: CellInteger, Text: text}, nil
	case pgtype.NumericOID, pgtype.Float4OID, pgtype.Float8OID:
		return Cell{Kind: CellDecimal, Text: text}, nil
	case pgtype.JSONOID, pgtype.JSONBOID:
		canonical, err := CanonicalJSON(raw)
		if err != nil {
			return Cell{}, err
		}
		return Cell{Kind: CellJSON, Text: string(canonical)}, nil
	case pgtype.ByteaOID:
		if !strings.HasPrefix(text, `\x`) {
			return Cell{}, errors.New("unexpected bytea text format")
		}
		decoded, err := hex.DecodeString(text[2:])
		if err != nil {
			return Cell{}, err
		}
		return Cell{Kind: CellBytes, Text: base64.StdEncoding.EncodeToString(decoded)}, nil
	case pgtype.DateOID, pgtype.TimeOID, pgtype.TimetzOID, pgtype.TimestampOID, pgtype.TimestamptzOID, pgtype.IntervalOID:
		return Cell{Kind: CellTemporal, Text: text}, nil
	default:
		if _, ok := typeMap.TypeForOID(oid); !ok {
			return Cell{}, fmt.Errorf("unsupported PostgreSQL result type OID %d", oid)
		}
		return Cell{Kind: CellString, Text: text}, nil
	}
}

func (runner *Runner) reconcileReceipt(ctx context.Context, input RunInput, result Result) (bool, error) {
	reconcileContext, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var rowCount int
	var affectedRows int64
	var resultBytes int
	err := runner.control.QueryRow(reconcileContext, `select row_count,affected_rows,result_bytes
		from pgbase_meta.sql_console_write_receipts
		where execution_id=$1 and administrator_id=$2 and idempotency_hash=$3 and request_hash=$4`,
		input.Request.ExecutionID, input.AdministratorID, input.IdempotencyHash[:], input.RequestHash[:]).Scan(
		&rowCount, &affectedRows, &resultBytes,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if rowCount != result.Rows || affectedRows != result.AffectedRows || resultBytes != result.Bytes {
		return false, ErrReceiptMismatch
	}
	return true, nil
}

func SQLState(err error) string {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		return postgresError.Code
	}
	return ""
}
