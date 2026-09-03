// Package testsupport contains bounded helpers shared by black-box test suites.
package testsupport

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Poll evaluates condition immediately and then at interval until it succeeds,
// its total timeout expires, or the parent context is canceled. Each condition
// receives a bounded child context so a single external probe cannot consume
// the complete polling budget.
func Poll(
	ctx context.Context,
	timeout time.Duration,
	interval time.Duration,
	attemptTimeout time.Duration,
	condition func(context.Context) (bool, error),
) error {
	if ctx == nil || timeout <= 0 || interval <= 0 || attemptTimeout <= 0 || condition == nil {
		return errors.New("poll: invalid configuration")
	}
	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error
	for {
		if err := pollCtx.Err(); err != nil {
			return pollFailure(err, lastErr)
		}
		attemptCtx, cancelAttempt := context.WithTimeout(pollCtx, attemptTimeout)
		done, err := condition(attemptCtx)
		cancelAttempt()
		if err != nil {
			lastErr = err
		}
		if err := pollCtx.Err(); err != nil {
			return pollFailure(err, lastErr)
		}
		if done {
			return nil
		}

		timer := time.NewTimer(interval)
		select {
		case <-timer.C:
		case <-pollCtx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return pollFailure(pollCtx.Err(), lastErr)
		}
	}
}

func pollFailure(cause, lastErr error) error {
	if lastErr == nil {
		return fmt.Errorf("poll: %w", cause)
	}
	return errors.Join(fmt.Errorf("poll: %w", cause), fmt.Errorf("last poll attempt: %w", lastErr))
}
