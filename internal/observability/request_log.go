package observability

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yakisoba0728/PgBase/internal/database"
)

const recentRequestCapacity = 1000

// DefaultRequestLogMaximumRows is the row cap applied when none is configured.
const DefaultRequestLogMaximumRows = 100_000

const (
	// defaultRequestLogPruneRows bounds one cleanup statement per policy: at
	// most this many expired rows and this many rows over the cap. A backlog
	// left by a lowered retention or by failed cleanups is then removed over
	// several batches instead of by one delete that outlives every budget
	// and never commits.
	defaultRequestLogPruneRows = 5000
	// requestLogPruneWarningInterval spaces the repeated warning while cleanup
	// keeps failing.
	requestLogPruneWarningInterval = 5 * time.Minute
)

var ErrRequestLogNotFound = errors.New("request log entry not found")
var ErrRequestLogClosed = errors.New("request log is shut down")

type RequestLogOptions struct {
	Retention                             time.Duration
	MaximumRows, QueueCapacity, BatchSize int
	FlushInterval                         time.Duration
	// RuntimeRetention returns the age limit and revision of one settings snapshot.
	RuntimeRetention func() (time.Duration, int64)
	// Logger receives the warning when cleanup fails and the notice when it
	// recovers. Nil discards them.
	Logger *slog.Logger
	// Recorder receives one request_log_prune operation event per cleanup.
	Recorder Recorder

	pruneRows int
}

func (o RequestLogOptions) normalized() RequestLogOptions {
	if o.Retention <= 0 {
		o.Retention = 7 * 24 * time.Hour
	}
	if o.MaximumRows <= 0 {
		o.MaximumRows = DefaultRequestLogMaximumRows
	}
	if o.QueueCapacity <= 0 {
		o.QueueCapacity = 4096
	}
	if o.BatchSize <= 0 || o.BatchSize > o.QueueCapacity {
		o.BatchSize = min(128, o.QueueCapacity)
	}
	if o.FlushInterval <= 0 {
		o.FlushInterval = time.Second
	}
	if o.pruneRows <= 0 {
		o.pruneRows = defaultRequestLogPruneRows
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return o
}

// RequestLog always maintains the previous 1,000-entry ring. NewRequestLog
// additionally persists entries through a bounded asynchronous writer.
type RequestLog struct {
	mu          sync.RWMutex
	entries     [recentRequestCapacity]RequestEntry
	sequence    uint64
	pool        *database.Pool
	options     RequestLogOptions
	queue       chan RequestEntry
	flush       chan chan error
	stop        chan struct{}
	shutdownErr error
	done        chan struct{}
	dropped     atomic.Uint64
	closed      atomic.Bool
	lifecycle   sync.RWMutex

	pruneMu       sync.Mutex
	pruneFailures int       // guarded by pruneMu: consecutive failed cleanups
	pruneWarnedAt time.Time // guarded by pruneMu
}

type RequestEntry struct {
	ID         uint64    `json:"id"`
	At         time.Time `json:"at"`
	Method     string    `json:"method"`
	Route      string    `json:"route"`
	Status     int       `json:"status"`
	DurationMS int64     `json:"durationMs"`
	ClientIP   string    `json:"clientIP,omitempty"`
	AuthKind   string    `json:"authKind,omitempty"`
	AuthID     string    `json:"authID,omitempty"`
}

type RequestPage struct {
	Items      []RequestEntry `json:"items"`
	NextBefore uint64         `json:"nextBefore,omitempty"`
	OldestID   uint64         `json:"oldestId"`
}
type RequestFilter struct {
	Before        uint64
	StatusClass   int
	Method, Route string
	// Query is a case-insensitive substring of the route, client IP or auth
	// identity.
	Query    string
	From, To *time.Time
	Limit    int
}
type RequestStatistics struct {
	Total             uint64            `json:"total"`
	Dropped           uint64            `json:"dropped"`
	OldestAt          *time.Time        `json:"oldestAt,omitempty"`
	NewestAt          *time.Time        `json:"newestAt,omitempty"`
	AverageDurationMS float64           `json:"averageDurationMs"`
	StatusClasses     map[string]uint64 `json:"statusClasses"`
}

func NewRequestLog(pool *database.Pool, options RequestLogOptions) *RequestLog {
	options = options.normalized()
	l := &RequestLog{pool: pool, options: options, queue: make(chan RequestEntry, options.QueueCapacity), flush: make(chan chan error), stop: make(chan struct{}), done: make(chan struct{})}
	go l.writeLoop()
	return l
}

func (l *RequestLog) Record(method, route string, status int, elapsed time.Duration) {
	l.RecordContext(method, route, status, elapsed, "", "", "")
}

func (l *RequestLog) RecordContext(method, route string, status int, elapsed time.Duration, clientIP, authKind, authID string) {
	if l == nil {
		return
	}
	if len(method) > 16 {
		method = "OTHER"
	}
	if len(route) > 256 {
		route = "unmatched"
	}
	e := RequestEntry{At: time.Now().UTC(), Method: method, Route: route, Status: status, DurationMS: elapsed.Milliseconds(), ClientIP: clientIP, AuthKind: authKind, AuthID: authID}
	l.mu.Lock()
	l.sequence++
	e.ID = l.sequence
	l.entries[(l.sequence-1)%recentRequestCapacity] = e
	l.mu.Unlock()
	if l.queue != nil && !l.closed.Load() {
		l.lifecycle.RLock()
		defer l.lifecycle.RUnlock()
		if l.closed.Load() {
			l.dropped.Add(1)
			return
		}
		select {
		case l.queue <- e:
		default:
			l.dropped.Add(1)
		}
	}
}

func (l *RequestLog) Dropped() uint64 {
	if l == nil {
		return 0
	}
	return l.dropped.Load()
}

func (l *RequestLog) Page(before uint64, statusClass int) RequestPage {
	return l.searchRecent(RequestFilter{Before: before, StatusClass: statusClass, Limit: 50})
}

func (l *RequestLog) Search(ctx context.Context, f RequestFilter) (RequestPage, error) {
	if l.pool == nil {
		return l.searchRecent(f), nil
	}
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	method, route := strings.ToUpper(strings.TrimSpace(f.Method)), strings.TrimSpace(f.Route)
	rows, err := l.pool.Query(ctx, `SELECT id,occurred_at,method,route,status,duration_ms,coalesce(host(client_ip),''),coalesce(auth_kind,''),coalesce(auth_id::text,'') FROM pgbase_meta.request_logs WHERE ($1::bigint=0 OR id<$1) AND ($2::integer=0 OR status/100=$2) AND ($3='' OR method=$3) AND ($4='' OR route=$4) AND ($5::timestamptz IS NULL OR occurred_at >= $5) AND ($6::timestamptz IS NULL OR occurred_at < $6) AND ($8='' OR search_text LIKE $8 ESCAPE '\') ORDER BY id DESC LIMIT $7`, f.Before, f.StatusClass, method, route, f.From, f.To, limit+1, requestSearchPattern(f.Query))
	if err != nil {
		return RequestPage{}, err
	}
	defer rows.Close()
	p := RequestPage{Items: make([]RequestEntry, 0, limit)}
	for rows.Next() {
		var e RequestEntry
		if err := rows.Scan(&e.ID, &e.At, &e.Method, &e.Route, &e.Status, &e.DurationMS, &e.ClientIP, &e.AuthKind, &e.AuthID); err != nil {
			return p, err
		}
		if len(p.Items) == limit {
			p.NextBefore = p.Items[len(p.Items)-1].ID
			break
		}
		p.Items = append(p.Items, e)
	}
	if err := rows.Err(); err != nil {
		return p, err
	}
	rows.Close()
	if err := l.pool.QueryRow(ctx, `SELECT COALESCE(min(id),0) FROM pgbase_meta.request_logs`).Scan(&p.OldestID); err != nil {
		return RequestPage{}, err
	}
	return p, nil
}

func (l *RequestLog) searchRecent(f RequestFilter) RequestPage {
	l.mu.RLock()
	defer l.mu.RUnlock()
	p := RequestPage{Items: make([]RequestEntry, 0, 50)}
	if l.sequence == 0 {
		return p
	}
	oldest := uint64(1)
	if l.sequence > recentRequestCapacity {
		oldest = l.sequence - recentRequestCapacity + 1
	}
	p.OldestID = oldest
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	for id := l.sequence; id >= oldest; id-- {
		if f.Before != 0 && id >= f.Before {
			continue
		}
		e := l.entries[(id-1)%recentRequestCapacity]
		if f.StatusClass != 0 && e.Status/100 != f.StatusClass {
			continue
		}
		if f.Method != "" && !strings.EqualFold(e.Method, f.Method) {
			continue
		}
		if f.Route != "" && e.Route != f.Route {
			continue
		}
		if f.From != nil && e.At.Before(*f.From) {
			continue
		}
		if f.To != nil && !e.At.Before(*f.To) {
			continue
		}
		if f.Query != "" && !strings.Contains(strings.ToLower(strings.Join([]string{e.Route, e.ClientIP, e.AuthKind, e.AuthID}, " ")), strings.ToLower(f.Query)) {
			continue
		}
		if len(p.Items) == limit {
			p.NextBefore = p.Items[len(p.Items)-1].ID
			break
		}
		p.Items = append(p.Items, e)
	}
	return p
}

func (l *RequestLog) Detail(ctx context.Context, id uint64) (RequestEntry, error) {
	if l.pool == nil {
		l.mu.RLock()
		defer l.mu.RUnlock()
		oldest := uint64(1)
		if l.sequence > recentRequestCapacity {
			oldest = l.sequence - recentRequestCapacity + 1
		}
		if id < oldest || id > l.sequence {
			return RequestEntry{}, ErrRequestLogNotFound
		}
		e := l.entries[(id-1)%recentRequestCapacity]
		if e.ID != id {
			return RequestEntry{}, ErrRequestLogNotFound
		}
		return e, nil
	}
	var e RequestEntry
	err := l.pool.QueryRow(ctx, `SELECT id,occurred_at,method,route,status,duration_ms,coalesce(host(client_ip),''),coalesce(auth_kind,''),coalesce(auth_id::text,'') FROM pgbase_meta.request_logs WHERE id=$1`, id).Scan(&e.ID, &e.At, &e.Method, &e.Route, &e.Status, &e.DurationMS, &e.ClientIP, &e.AuthKind, &e.AuthID)
	if errors.Is(err, pgx.ErrNoRows) {
		return RequestEntry{}, ErrRequestLogNotFound
	}
	if err != nil {
		return RequestEntry{}, fmt.Errorf("read request log entry %d: %w", id, err)
	}
	return e, nil
}

// Statistics covers [from, to); nil bounds are open.
func (l *RequestLog) Statistics(ctx context.Context, from, to *time.Time) (RequestStatistics, error) {
	s := RequestStatistics{Dropped: l.Dropped(), StatusClasses: map[string]uint64{}}
	if l.pool == nil {
		l.mu.RLock()
		defer l.mu.RUnlock()
		oldest := uint64(1)
		if l.sequence > recentRequestCapacity {
			oldest = l.sequence - recentRequestCapacity + 1
		}
		for id := oldest; id <= l.sequence; id++ {
			e := l.entries[(id-1)%recentRequestCapacity]
			if (from != nil && e.At.Before(*from)) || (to != nil && !e.At.Before(*to)) {
				continue
			}
			s.Total++
			s.StatusClasses[fmt.Sprintf("%dxx", e.Status/100)]++
			if s.OldestAt == nil || e.At.Before(*s.OldestAt) {
				at := e.At
				s.OldestAt = &at
			}
			if s.NewestAt == nil || e.At.After(*s.NewestAt) {
				at := e.At
				s.NewestAt = &at
			}
			s.AverageDurationMS += float64(e.DurationMS)
		}
		if s.Total > 0 {
			s.AverageDurationMS /= float64(s.Total)
		}
		return s, nil
	}
	// Bounds are added only when present so the occurred_at index stays usable
	// under a cached generic plan.
	where, args := "", []any{}
	for _, bound := range []struct {
		value *time.Time
		op    string
	}{{from, ">="}, {to, "<"}} {
		if bound.value != nil {
			args = append(args, *bound.value)
			where += fmt.Sprintf(" AND occurred_at %s $%d", bound.op, len(args))
		}
	}
	if where != "" {
		where = " WHERE true" + where
	}
	var avg *float64
	if err := l.pool.QueryRow(ctx, `SELECT count(*),min(occurred_at),max(occurred_at),avg(duration_ms) FROM pgbase_meta.request_logs`+where, args...).Scan(&s.Total, &s.OldestAt, &s.NewestAt, &avg); err != nil {
		return s, err
	}
	if avg != nil {
		s.AverageDurationMS = *avg
	}
	rows, err := l.pool.Query(ctx, `SELECT status/100,count(*) FROM pgbase_meta.request_logs`+where+` GROUP BY status/100 ORDER BY status/100`, args...)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var c int
		var n uint64
		if err := rows.Scan(&c, &n); err != nil {
			return s, err
		}
		s.StatusClasses[fmt.Sprintf("%dxx", c)] = n
	}
	return s, rows.Err()
}

func (l *RequestLog) Truncate(ctx context.Context) error {
	if l.pool != nil {
		if err := l.Flush(ctx); err != nil {
			return err
		}
		_, err := l.pool.Exec(ctx, `TRUNCATE pgbase_meta.request_logs RESTART IDENTITY`)
		return err
	}
	l.mu.Lock()
	l.entries = [recentRequestCapacity]RequestEntry{}
	l.sequence = 0
	l.mu.Unlock()
	return nil
}

// PruneExpired applies the configured age limit without enforcing the row cap.
// It deletes in bounded statements until no expired row is left or ctx ends,
// so a deadline still keeps the rows removed before it.
func (l *RequestLog) PruneExpired(ctx context.Context) error {
	if l == nil || l.pool == nil {
		return nil
	}
	started := time.Now()
	var total int64
	for {
		deleted, err := l.prune(ctx, l.pool, 0)
		total += deleted
		if err != nil || deleted < int64(l.options.pruneRows) {
			l.observePrune(ctx, started, total, err)
			return err
		}
	}
}

// prune deletes at most pruneRows expired rows and pruneRows rows over
// maximumRows, oldest first, and reports how many it deleted.
func (l *RequestLog) prune(ctx context.Context, db requestLogExecutor, maximumRows int) (int64, error) {
	retention, revision := l.options.Retention, int64(0)
	if source := l.options.RuntimeRetention; source != nil {
		retention, revision = source()
		if retention < 24*time.Hour || revision < 1 {
			return 0, errors.New("invalid runtime request log retention")
		}
	}
	// Lock the matching settings row through the delete. A concurrent settings
	// update either follows this cleanup or makes its age policy stale. Both
	// candidate sets are read in index order (occurred_at, then id) and stop at
	// the limit, so an empty set costs one index probe and a backlog costs at
	// most the limit.
	result, err := db.Exec(ctx, `WITH current_policy AS MATERIALIZED (
		SELECT revision FROM pgbase_meta.runtime_settings WHERE singleton AND revision=$3 FOR SHARE
	), expired AS (
		SELECT id FROM pgbase_meta.request_logs
		WHERE occurred_at < statement_timestamp()-make_interval(secs=>$1)
			AND ($3=0 OR EXISTS (SELECT 1 FROM current_policy))
		ORDER BY occurred_at LIMIT $4
	), excess AS (
		SELECT id FROM pgbase_meta.request_logs
		WHERE $2::bigint>0 AND id <= GREATEST(0,(SELECT max(id) FROM pgbase_meta.request_logs)-$2)
		ORDER BY id LIMIT $4
	)
	DELETE FROM pgbase_meta.request_logs
	WHERE id IN (SELECT id FROM expired UNION ALL SELECT id FROM excess)`,
		int64(retention.Seconds()), maximumRows, revision, l.options.pruneRows)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

// observePrune records one cleanup. While cleanups keep failing the table only
// grows, so the first failure warns at once and later ones at most every
// requestLogPruneWarningInterval; the first success after them says so.
func (l *RequestLog) observePrune(ctx context.Context, started time.Time, deleted int64, err error) {
	outcome := requestLogPruneOutcome(err)
	if l.options.Recorder != nil {
		l.options.Recorder.Record(ctx, OperationEvent(OperationRequestLogPrune, outcome, time.Since(started)))
	}
	l.pruneMu.Lock()
	defer l.pruneMu.Unlock()
	if err == nil {
		if l.pruneFailures > 0 {
			l.options.Logger.InfoContext(ctx, "request log cleanup recovered",
				"failed_attempts", l.pruneFailures, "deleted", deleted)
		}
		l.pruneFailures, l.pruneWarnedAt = 0, time.Time{}
		return
	}
	l.pruneFailures++
	now := time.Now()
	if l.pruneFailures > 1 && now.Sub(l.pruneWarnedAt) < requestLogPruneWarningInterval {
		return
	}
	l.pruneWarnedAt = now
	l.options.Logger.WarnContext(ctx, "request log cleanup failed; the table grows past its retention until a cleanup succeeds",
		"outcome", string(outcome), "consecutive_failures", l.pruneFailures, "deleted", deleted, "error", err)
}

func requestLogPruneOutcome(err error) Outcome {
	var pgErr *pgconn.PgError
	switch {
	case err == nil:
		return OutcomeSuccess
	case errors.Is(err, context.Canceled):
		return OutcomeCanceled
	// 57014 is a statement timeout (or cancel), 55P03 a lock timeout.
	case errors.Is(err, context.DeadlineExceeded),
		errors.As(err, &pgErr) && (pgErr.Code == "57014" || pgErr.Code == "55P03"):
		return OutcomeBudget
	default:
		return OutcomeFailure
	}
}

func (l *RequestLog) DeleteBefore(ctx context.Context, before time.Time) (int64, error) {
	if l == nil || l.pool == nil || before.IsZero() {
		return 0, errors.New("request log deletion is unavailable")
	}
	if err := l.Flush(ctx); err != nil {
		return 0, err
	}
	result, err := l.pool.Exec(ctx, `DELETE FROM pgbase_meta.request_logs WHERE occurred_at < $1`, before.UTC())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}
func (l *RequestLog) Flush(ctx context.Context) error {
	if l == nil || l.flush == nil {
		return nil
	}
	if l.closed.Load() {
		return ErrRequestLogClosed
	}
	ack := make(chan error, 1)
	select {
	case l.flush <- ack:
	case <-l.done:
		return ErrRequestLogClosed
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-ack:
		return err
	case <-l.done:
		select {
		case err := <-ack:
			return err
		default:
			return ErrRequestLogClosed
		}
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *RequestLog) Shutdown(ctx context.Context) error {
	if l == nil || l.stop == nil {
		return nil
	}
	l.lifecycle.Lock()
	if !l.closed.Swap(true) {
		close(l.stop)
	}
	l.lifecycle.Unlock()
	select {
	case <-l.done:
		return l.shutdownErr
	default:
	}
	select {
	case <-l.done:
		return l.shutdownErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *RequestLog) writeLoop() {
	defer close(l.done)
	timer := time.NewTimer(l.options.FlushInterval)
	defer timer.Stop()
	batch := make([]RequestEntry, 0, l.options.BatchSize)
	write := func() error {
		if len(batch) == 0 {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := l.writeBatch(ctx, batch)
		batch = batch[:0]
		return err
	}
	flush := func() error {
		// Snapshot the FIFO watermark. Continuous traffic must not keep a flush
		// or shutdown acknowledgement waiting forever for the queue to empty.
		pending := len(l.queue)
		var result error
		for pending > 0 {
			for pending > 0 && len(batch) < l.options.BatchSize {
				batch = append(batch, <-l.queue)
				pending--
			}
			result = errors.Join(result, write())
		}
		return errors.Join(result, write())
	}
	for {
		select {
		case e := <-l.queue:
			batch = append(batch, e)
			if len(batch) >= l.options.BatchSize {
				_ = write()
			}
		case ack := <-l.flush:
			ack <- flush()
		case <-l.stop:
			l.shutdownErr = flush()
			return
		case <-timer.C:
			_ = flush()
			timer.Reset(l.options.FlushInterval)
		}
	}
}

// requestLogWriteOptions commits a batch and its cleanup without waiting for
// a WAL flush. A database crash can lose only the last best-effort diagnostic
// batches, like a full queue would. The isolation stays read committed so the
// cleanup still rechecks the settings revision after waiting.
var requestLogWriteOptions = pgx.TxOptions{BeginQuery: "begin; set local synchronous_commit = off"}

// requestLogPruneBudget leaves half of the remaining batch deadline for the
// rollback to the savepoint and the commit. Without a deadline nothing can
// close the connection underneath the batch, so the cleanup gets a fixed bound.
func requestLogPruneBudget(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 5 * time.Second
	}
	budget := time.Until(deadline) / 2
	if budget < time.Millisecond {
		return 0
	}
	return budget
}

type requestLogExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// writeBatch counts every entry it could not store as dropped.
func (l *RequestLog) writeBatch(ctx context.Context, entries []RequestEntry) error {
	if len(entries) == 0 {
		return nil
	}
	var pruneErr error
	var pruned int64
	var pruneStarted time.Time
	failed := l.pool.InTx(ctx, requestLogWriteOptions, func(tx pgx.Tx) error {
		if err := insertEntries(ctx, tx, entries); err != nil {
			return err
		}
		// A cleanup failure must not roll back the stored entries. A cleanup
		// that outlives ctx would make pgx close the connection and lose the
		// batch with it, so the server ends a slow cleanup first: a statement
		// timeout aborts only the savepoint and leaves the batch to commit.
		pruneStarted, pruned = time.Now(), 0
		budget := requestLogPruneBudget(ctx)
		if budget <= 0 {
			pruneErr = context.DeadlineExceeded
			return nil
		}
		pruneErr = database.InSavepoint(ctx, tx, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, fmt.Sprintf("set local statement_timeout = %d", budget.Milliseconds())); err != nil {
				return err
			}
			deleted, err := l.prune(ctx, tx, l.options.MaximumRows)
			pruned = deleted
			return err
		})
		if pruneErr != nil {
			pruned = 0
		}
		return nil
	})
	if failed == nil {
		l.observePrune(ctx, pruneStarted, pruned, pruneErr)
		return pruneErr
	}
	if len(entries) == 1 || ctx.Err() != nil {
		l.dropped.Add(uint64(len(entries)))
		return failed
	}
	// Retry row by row so one malformed entry cannot lose the rest of the batch.
	failed = nil
	for index := range entries {
		if err := insertEntries(ctx, l.pool, entries[index:index+1]); err != nil {
			l.dropped.Add(1)
			failed = err
		}
	}
	pruneStarted = time.Now()
	pruned, pruneErr = l.prune(ctx, l.pool, l.options.MaximumRows)
	l.observePrune(ctx, pruneStarted, pruned, pruneErr)
	return errors.Join(failed, pruneErr)
}

func insertEntries(ctx context.Context, db requestLogExecutor, entries []RequestEntry) error {
	values := make([]any, 0, len(entries)*8)
	var q strings.Builder
	q.WriteString(`INSERT INTO pgbase_meta.request_logs(occurred_at,method,route,status,duration_ms,client_ip,auth_kind,auth_id) VALUES `)
	for i, e := range entries {
		if i > 0 {
			q.WriteByte(',')
		}
		b := i * 8
		fmt.Fprintf(&q, "($%d,$%d,$%d,$%d,$%d,nullif($%d,'')::inet,nullif($%d,''),nullif($%d,'')::uuid)", b+1, b+2, b+3, b+4, b+5, b+6, b+7, b+8)
		values = append(values, e.At, e.Method, e.Route, e.Status, e.DurationMS, e.ClientIP, e.AuthKind, e.AuthID)
	}
	_, err := db.Exec(ctx, q.String(), values...)
	return err
}

// Escape LIKE metacharacters so searches remain literal substrings.
func requestSearchPattern(query string) string {
	if query == "" {
		return ""
	}
	return "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(strings.ToLower(query)) + "%"
}
