package recordops

import (
	"context"
	"errors"
	"sync"

	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/membership"
)

// membershipWriterProvider keeps runtime registration outside record
// transactions while hiding whether the runtime was injected by the
// application or obtained lazily by a direct constructor.
type membershipWriterProvider struct {
	pool *database.Pool

	mu       sync.Mutex
	runtime  *membership.Runtime
	injected bool
}

type transactionalMembershipError struct {
	cause error
}

func (e *transactionalMembershipError) Error() string {
	return "record membership transaction failed"
}

func (e *transactionalMembershipError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func newMembershipWriterProvider(pool *database.Pool, runtime *membership.Runtime) *membershipWriterProvider {
	return &membershipWriterProvider{pool: pool, runtime: runtime, injected: runtime != nil}
}

func (p *membershipWriterProvider) writer(ctx context.Context) (membership.Writer, error) {
	if p == nil || p.pool == nil {
		return membership.Writer{}, membership.ErrUnavailable
	}

	p.mu.Lock()
	runtime := p.runtime
	injected := p.injected
	if runtime == nil {
		var err error
		runtime, err = membership.RegisterWriter(ctx, membership.New(p.pool), membership.WriterA)
		if err != nil {
			// Registration failures are deliberately not cached. A direct service
			// constructed before migration M can succeed once the exact v7
			// installation becomes available.
			p.mu.Unlock()
			return membership.Writer{}, err
		}
		p.runtime = runtime
		p.mu.Unlock()
		if runtime.WriterVersion() < membership.WriterA || runtime.WriterVersion() > membership.WriterC {
			return membership.Writer{}, membership.ErrWriterTooOld
		}
		return runtime.Writer()
	}
	p.mu.Unlock()

	if runtime.WriterVersion() < membership.WriterA || runtime.WriterVersion() > membership.WriterC {
		return membership.Writer{}, membership.ErrWriterTooOld
	}
	if injected {
		return runtime.Writer()
	}
	// Refreshing at each mutation gives direct constructors a current lease;
	// application runtimes still keep their independent background heartbeat.
	if err := runtime.Ready(ctx); err != nil {
		return membership.Writer{}, err
	}
	return runtime.Writer()
}

// unavailableMembership is the recordops seam for the membership control
// plane. Membership sentinels and database details are deliberately not kept
// as an unwrap-able cause because callers only need the stable availability
// classification and must not learn rollout state.
func unavailableMembership(err error) error {
	if err == nil {
		return nil
	}
	return &Error{
		Code:     CodeUnavailable,
		canceled: errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded),
	}
}

// transactionalMembership keeps PostgreSQL retry classification available to
// the transaction kernel. membershipBoundaryError removes that cause before
// the error leaves recordops.
func transactionalMembership(err error) error {
	if err == nil {
		return nil
	}
	return &transactionalMembershipError{cause: err}
}

func membershipBoundaryError(err error) error {
	var membershipError *transactionalMembershipError
	if !errors.As(err, &membershipError) {
		return err
	}
	return unavailableMembership(membershipError.cause)
}
