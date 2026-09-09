package observability

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/webisnoteasy/PgBase/internal/database"
)

const recentRequestCapacity = 1000

var ErrRequestLogNotFound = errors.New("request log entry not found")
var ErrRequestLogClosed = errors.New("request log is shut down")

type RequestLogOptions struct {
	Retention                             time.Duration
	MaximumRows, QueueCapacity, BatchSize int
	FlushInterval                         time.Duration
}

func (o RequestLogOptions) normalized() RequestLogOptions {
	if o.Retention <= 0 {
		o.Retention = 7 * 24 * time.Hour
	}
	if o.MaximumRows <= 0 {
		o.MaximumRows = 100_000
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
	flush, stop chan chan error
	done        chan struct{}
	dropped     atomic.Uint64
	closed      atomic.Bool
	lifecycle   sync.RWMutex
}

type RequestEntry struct {
	ID         uint64    `json:"id"`
	At         time.Time `json:"at"`
	Method     string    `json:"method"`
	Route      string    `json:"route"`
	Status     int       `json:"status"`
	DurationMS int64     `json:"durationMs"`
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
	From, To      *time.Time
	Limit         int
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
	l := &RequestLog{pool: pool, options: options, queue: make(chan RequestEntry, options.QueueCapacity), flush: make(chan chan error), stop: make(chan chan error, 1), done: make(chan struct{})}
	go l.writeLoop()
	return l
}

func (l *RequestLog) Record(method, route string, status int, elapsed time.Duration) {
	if l == nil {
		return
	}
	if len(method) > 16 {
		method = "OTHER"
	}
	if len(route) > 256 {
		route = "unmatched"
	}
	e := RequestEntry{At: time.Now().UTC(), Method: method, Route: route, Status: status, DurationMS: elapsed.Milliseconds()}
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
	for id := l.sequence; id >= oldest; id-- {
		if before != 0 && id >= before {
			continue
		}
		e := l.entries[(id-1)%recentRequestCapacity]
		if statusClass != 0 && e.Status/100 != statusClass {
			continue
		}
		if len(p.Items) == 50 {
			p.NextBefore = p.Items[49].ID
			break
		}
		p.Items = append(p.Items, e)
	}
	return p
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
	rows, err := l.pool.Query(ctx, `SELECT id,occurred_at,method,route,status,duration_ms FROM pgbase_meta.request_logs WHERE ($1::bigint=0 OR id<$1) AND ($2::integer=0 OR status/100=$2) AND ($3='' OR method=$3) AND ($4='' OR route=$4) AND ($5::timestamptz IS NULL OR occurred_at >= $5) AND ($6::timestamptz IS NULL OR occurred_at < $6) ORDER BY id DESC LIMIT $7`, f.Before, f.StatusClass, method, route, f.From, f.To, limit+1)
	if err != nil {
		return RequestPage{}, err
	}
	defer rows.Close()
	p := RequestPage{Items: make([]RequestEntry, 0, limit)}
	for rows.Next() {
		var e RequestEntry
		if err := rows.Scan(&e.ID, &e.At, &e.Method, &e.Route, &e.Status, &e.DurationMS); err != nil {
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
	_ = l.pool.QueryRow(ctx, `SELECT COALESCE(min(id),0) FROM pgbase_meta.request_logs`).Scan(&p.OldestID)
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
	err := l.pool.QueryRow(ctx, `SELECT id,occurred_at,method,route,status,duration_ms FROM pgbase_meta.request_logs WHERE id=$1`, id).Scan(&e.ID, &e.At, &e.Method, &e.Route, &e.Status, &e.DurationMS)
	if err != nil {
		return RequestEntry{}, ErrRequestLogNotFound
	}
	return e, nil
}

func (l *RequestLog) Statistics(ctx context.Context) (RequestStatistics, error) {
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
	var avg *float64
	if err := l.pool.QueryRow(ctx, `SELECT count(*),min(occurred_at),max(occurred_at),avg(duration_ms) FROM pgbase_meta.request_logs`).Scan(&s.Total, &s.OldestAt, &s.NewestAt, &avg); err != nil {
		return s, err
	}
	if avg != nil {
		s.AverageDurationMS = *avg
	}
	rows, err := l.pool.Query(ctx, `SELECT status/100,count(*) FROM pgbase_meta.request_logs GROUP BY status/100 ORDER BY status/100`)
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
func (l *RequestLog) Flush(ctx context.Context) error {
	if l == nil || l.flush == nil {
		return nil
	}
	l.lifecycle.RLock()
	if l.closed.Load() {
		l.lifecycle.RUnlock()
		return ErrRequestLogClosed
	}
	ack := make(chan error, 1)
	select {
	case l.flush <- ack:
		l.lifecycle.RUnlock()
	case <-ctx.Done():
		l.lifecycle.RUnlock()
		return ctx.Err()
	}
	select {
	case err := <-ack:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (l *RequestLog) Shutdown(ctx context.Context) error {
	if l == nil || l.stop == nil {
		return nil
	}
	l.lifecycle.Lock()
	if !l.closed.CompareAndSwap(false, true) {
		l.lifecycle.Unlock()
		select {
		case <-l.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	l.lifecycle.Unlock()
	ack := make(chan error, 1)
	l.stop <- ack
	select {
	case err := <-ack:
		return err
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
		if err != nil {
			l.dropped.Add(uint64(len(batch)))
		}
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
		case ack := <-l.stop:
			ack <- flush()
			return
		case <-timer.C:
			_ = flush()
			timer.Reset(l.options.FlushInterval)
		}
	}
}

func (l *RequestLog) writeBatch(ctx context.Context, entries []RequestEntry) error {
	if len(entries) == 0 {
		return nil
	}
	values := make([]any, 0, len(entries)*5)
	var q strings.Builder
	q.WriteString(`INSERT INTO pgbase_meta.request_logs(occurred_at,method,route,status,duration_ms) VALUES `)
	for i, e := range entries {
		if i > 0 {
			q.WriteByte(',')
		}
		b := i * 5
		fmt.Fprintf(&q, "($%d,$%d,$%d,$%d,$%d)", b+1, b+2, b+3, b+4, b+5)
		values = append(values, e.At, e.Method, e.Route, e.Status, e.DurationMS)
	}
	if _, err := l.pool.Exec(ctx, q.String(), values...); err != nil {
		return err
	}
	_, err := l.pool.Exec(ctx, `DELETE FROM pgbase_meta.request_logs WHERE occurred_at < clock_timestamp()-make_interval(secs=>$1) OR id <= GREATEST(0,(SELECT max(id) FROM pgbase_meta.request_logs)-$2)`, int64(l.options.Retention.Seconds()), l.options.MaximumRows)
	return err
}
