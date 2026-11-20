package pgbase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/yakisoba0728/PgBase/internal/backup"
)

type backupStorageRecovery struct {
	controller *backup.StorageController
	logger     *slog.Logger
}

func (r backupStorageRecovery) Run(ctx context.Context) error {
	return sampleLoop(ctx, 30*time.Second, func(ctx context.Context) {
		pass, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := r.controller.RecoverPending(pass); err != nil && ctx.Err() == nil {
			r.logger.WarnContext(ctx, "backup publication recovery deferred", "error", err)
		}
	})
}

func (a *App) startBackground(ctx context.Context) <-chan struct{} {
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	a.closeMu.Lock()
	a.backgroundCancel = cancel
	a.backgroundDone = done
	a.closeMu.Unlock()
	go func() {
		runners := make([]namedBackgroundRunner, 0, 12)
		if a.backupGate != nil && a.backupGate.Active() {
			runners = append(runners, namedBackgroundRunner{"PostgreSQL writer gate", a.backupGate})
		}
		if a.runtimeSettings != nil {
			runners = append(runners, namedBackgroundRunner{"runtime settings refresh", runtimeSettingsRefresher{a.runtimeSettings, a.options.Logger, a.backupSchedule}})
		}
		if a.options.Backups.controller != nil {
			runners = append(runners, namedBackgroundRunner{"backup publication recovery", backupStorageRecovery{a.options.Backups.controller, a.options.Logger}})
		}
		for _, candidate := range []namedBackgroundRunner{
			{"administrator authentication cleaner", a.cleaner},
			{"record idempotency cleaner", a.recordCleaner},
			{"record outbox delivery", a.realtimeWorker},
			{"scheduled jobs", a.jobWorker},
			{"file object maintenance", a.fileWorker},
			{"file storage migration", a.storageMigrationWorker},
			{"schema operation maintenance", a.schemaOperations},
			{"schema listener", a.schemaListener},
			{"record membership writer", a.membershipWriter},
			{"system status sampler", a.systemStatus},
			{"schema health sampler", a.schemaHealth},
			{"user migration source recovery", a.userMigrationSources},
		} {
			if candidate.runner != nil {
				runners = append(runners, candidate)
			}
		}
		results := make(chan backgroundResult, len(runners))
		for _, runner := range runners {
			go func(runner namedBackgroundRunner) {
				err := runner.runner.Run(runCtx)
				// pgx may surface cancellation of an in-flight write as an I/O timeout.
				var timeout net.Error
				if runCtx.Err() != nil && errors.As(err, &timeout) && timeout.Timeout() {
					err = errors.Join(err, runCtx.Err())
				}
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
