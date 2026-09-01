package database

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	maximumRetries    = 32
	maximumRetryDelay = 100 * time.Millisecond
)

type RetryCause string

const (
	RetryCauseCachedPlan    RetryCause = "cached_plan"
	RetryCauseSerialization RetryCause = "serialization"
	RetryCauseDeadlock      RetryCause = "deadlock"
)

// Retry applies the one PostgreSQL retry policy shared by transaction-owning
// kernels. The operation retains ownership of isolation and lock ordering.
func Retry(ctx context.Context, operation func() error, observe func(RetryCause, bool)) error {
	return retry(ctx, operation, observe, func(attempt int) error {
		timer := time.NewTimer(retryDelay(attempt))
		select {
		case <-timer.C:
			return ctx.Err()
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		}
	})
}

func retry(ctx context.Context, operation func() error, observe func(RetryCause, bool), wait func(int) error) error {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := operation()
		cause, retryable := retryCause(err)
		if err == nil || !retryable {
			return err
		}
		if attempt >= maximumRetries {
			if observe != nil {
				observe(cause, true)
			}
			return err
		}

		if err := wait(attempt); err != nil {
			return err
		}
		if observe != nil {
			observe(cause, false)
		}
	}
}

func retryDelay(attempt int) time.Duration {
	delay := 2 * time.Millisecond
	for step := 0; step < attempt && delay < maximumRetryDelay; step++ {
		delay *= 2
		if delay > maximumRetryDelay {
			delay = maximumRetryDelay
		}
	}
	half := delay / 2
	return half + time.Duration(rand.Int64N(int64(delay-half)+1))
}

func retryCause(err error) (RetryCause, bool) {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return "", false
	}
	switch postgresError.Code {
	case "0A000":
		return RetryCauseCachedPlan, true
	case "40001":
		return RetryCauseSerialization, true
	case "40P01":
		return RetryCauseDeadlock, true
	default:
		return "", false
	}
}
