package adminauth

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type rateLimitLockResult struct {
	acquired bool
	err      error
}

func (r rateLimitLockResult) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	*destinations[0].(*bool) = r.acquired
	return nil
}

type rateLimitLockDB struct {
	results []rateLimitLockResult
	queries []string
	keys    []int64
}

func (*rateLimitLockDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	panic("unexpected Exec")
}

func (*rateLimitLockDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("unexpected Query")
}

func (db *rateLimitLockDB) QueryRow(_ context.Context, query string, arguments ...any) pgx.Row {
	db.queries = append(db.queries, query)
	db.keys = append(db.keys, arguments[0].(int64))
	result := db.results[0]
	db.results = db.results[1:]
	return result
}

func TestStoreLockRateLimitsTriesKeysInOrderAndFailsFast(t *testing.T) {
	db := &rateLimitLockDB{results: []rateLimitLockResult{{acquired: true}, {acquired: false}, {acquired: true}}}
	err := NewStore(db).LockRateLimits(context.Background(), []int64{-8, 4, 11})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("LockRateLimits error = %v, want ErrRateLimited", err)
	}
	if want := []int64{-8, 4}; !reflect.DeepEqual(db.keys, want) {
		t.Fatalf("attempted lock keys = %v, want %v", db.keys, want)
	}
	for _, query := range db.queries {
		if query != "select pg_try_advisory_xact_lock($1)" {
			t.Fatalf("lock query = %q, want fail-fast transaction advisory lock", query)
		}
	}
}

func TestStoreLockRateLimitsWrapsQueryError(t *testing.T) {
	wantErr := errors.New("query failed")
	db := &rateLimitLockDB{results: []rateLimitLockResult{{err: wantErr}}}
	if err := NewStore(db).LockRateLimits(context.Background(), []int64{1}); !errors.Is(err, wantErr) {
		t.Fatalf("LockRateLimits error = %v, want wrapped query error", err)
	}
}
