package database

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// maximumReusableCoordinationSessions bounds the coordination sessions kept
// open between borrows. A borrower beyond it gets a dedicated session instead
// of waiting, so a slow download never queues behind another one.
const maximumReusableCoordinationSessions = 8

// coordinationSessionIdleTime closes a reusable session nobody borrowed for
// this long, so an idle server keeps no extra PostgreSQL backends.
const coordinationSessionIdleTime = time.Minute

const coordinationSessionResetTimeout = 3 * time.Second

// CoordinationSession is a borrowed session for short session-scoped advisory
// locks. Like OpenCoordinationSession it is outside the request pool and holds
// the backup-writer gate.
type CoordinationSession struct {
	conn   *pgx.Conn
	pooled *pgxpool.Conn
	slots  chan struct{}
	once   sync.Once
}

// Conn returns the session's connection. It belongs to the borrower until
// Release and must not be used afterwards.
func (s *CoordinationSession) Conn() *pgx.Conn { return s.conn }

// BorrowCoordinationSession returns a coordination session that Release can
// hand to the next borrower, saving the connection, TLS and authentication
// round trips of OpenCoordinationSession. The borrower must not keep any
// session state other than advisory locks, which Release drops.
func (p *Pool) BorrowCoordinationSession(ctx context.Context) (*CoordinationSession, error) {
	coordination, slots, err := p.coordinationPool()
	if err != nil {
		return nil, err
	}
	if coordination != nil {
		select {
		case slots <- struct{}{}:
			pooled, err := coordination.Acquire(ctx)
			if err != nil {
				<-slots
				return nil, err
			}
			return &CoordinationSession{conn: pooled.Conn(), pooled: pooled, slots: slots}, nil
		default:
		}
	}
	conn, err := p.OpenCoordinationSession(ctx)
	if err != nil {
		return nil, err
	}
	return &CoordinationSession{conn: conn}, nil
}

// Release ends the borrow and returns only after PostgreSQL has dropped the
// borrower's advisory locks. With reuse, a reusable session drops them, takes
// the backup-writer gate again, and goes back for the next borrower. Without
// reuse, when that reset fails, or for a dedicated session, the session is
// closed. Pass reuse=false whenever another goroutine may have closed the
// session's socket. Release is idempotent.
func (s *CoordinationSession) Release(reuse bool) {
	s.once.Do(func() {
		if s.pooled == nil {
			_ = CloseCoordinationSession(context.Background(), s.conn)
			return
		}
		defer func() { <-s.slots }()
		if reuse && resetCoordinationSession(s.conn) {
			s.pooled.Release()
			return
		}
		_ = CloseCoordinationSession(context.Background(), s.conn)
		// The pool destroys a closed connection instead of reusing it.
		s.pooled.Release()
	})
}

// resetCoordinationSession releases every advisory lock in one round trip and
// takes the backup-writer gate again. The derived table runs first: a volatile
// target list keeps PostgreSQL from flattening it into the outer query.
func resetCoordinationSession(conn *pgx.Conn) bool {
	ctx, cancel := context.WithTimeout(context.Background(), coordinationSessionResetTimeout)
	defer cancel()
	var admitted bool
	err := conn.QueryRow(ctx, `select pg_catalog.pg_try_advisory_lock_shared($1)
		from (select pg_catalog.pg_advisory_unlock_all()) as released`, backupGateKey).Scan(&admitted)
	return err == nil && admitted && conn.PgConn().TxStatus() == 'I'
}

// coordinationPool lazily creates the reusable sessions. After the pool starts
// closing it returns nil and borrowers fall back to dedicated sessions.
func (p *Pool) coordinationPool() (*pgxpool.Pool, chan struct{}, error) {
	if p == nil || p.inner == nil {
		return nil, nil, errors.New("database pool is unavailable")
	}
	p.coordinationMu.Lock()
	defer p.coordinationMu.Unlock()
	if p.coordinationClosed {
		return nil, nil, nil
	}
	if p.coordination == nil {
		config := p.inner.Config()
		config.AfterConnect = admitCoordinationSession
		config.BeforeAcquire = nil
		config.PrepareConn = nil
		config.AfterRelease = nil
		config.BeforeClose = nil
		config.MaxConns = maximumReusableCoordinationSessions
		config.MinConns = 0
		config.MinIdleConns = 0
		config.MaxConnIdleTime = coordinationSessionIdleTime
		config.HealthCheckPeriod = coordinationSessionIdleTime / 2
		config.PingTimeout = coordinationSessionResetTimeout
		coordination, err := pgxpool.NewWithConfig(context.Background(), config)
		if err != nil {
			return nil, nil, err
		}
		p.coordination = coordination
		p.coordinationSlots = make(chan struct{}, maximumReusableCoordinationSessions)
	}
	return p.coordination, p.coordinationSlots, nil
}
