package pgbase

import (
	"context"
	"errors"
	"fmt"
)

func (a *App) startBackground(ctx context.Context) <-chan struct{} {
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	a.closeMu.Lock()
	a.backgroundCancel = cancel
	a.backgroundDone = done
	a.closeMu.Unlock()
	go func() {
		runners := make([]namedBackgroundRunner, 0, 7)
		for _, candidate := range []namedBackgroundRunner{
			{"administrator authentication cleaner", a.cleaner},
			{"record idempotency cleaner", a.recordCleaner},
			{"record outbox delivery", a.realtimeWorker},
			{"scheduled jobs", a.jobWorker},
			{"file object maintenance", a.fileWorker},
			{"schema operation maintenance", a.schemaOperations},
			{"schema listener", a.schemaListener},
			{"record membership writer", a.membershipWriter},
			{"system status sampler", a.systemStatus},
			{"schema health sampler", a.schemaHealth},
		} {
			if candidate.runner != nil {
				runners = append(runners, candidate)
			}
		}
		results := make(chan backgroundResult, len(runners))
		for _, runner := range runners {
			go func(runner namedBackgroundRunner) {
				err := runner.runner.Run(runCtx)
				results <- backgroundResult{name: runner.name, err: err, cancellationErr: runCtx.Err()}
			}(runner)
		}
		var backgroundErr error
		for index := range runners {
			result := <-results
			switch {
			case result.err == nil && result.cancellationErr == nil:
				backgroundErr = errors.Join(backgroundErr, fmt.Errorf("%s stopped unexpectedly", result.name))
			case result.err != nil && !(result.cancellationErr != nil && errors.Is(result.err, result.cancellationErr)):
				backgroundErr = errors.Join(backgroundErr, fmt.Errorf("%s: %w", result.name, result.err))
			}
			if index == 0 {
				cancel()
			}
		}
		a.closeMu.Lock()
		a.backgroundErr = backgroundErr
		close(done)
		a.closeMu.Unlock()
	}()
	return done
}
