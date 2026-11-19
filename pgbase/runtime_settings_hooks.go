package pgbase

import (
	"context"
	"log/slog"
	"reflect"
	"time"

	"github.com/yakisoba0728/PgBase/internal/runtimesettings"
)

type runtimeSettingsRefresher struct {
	settings       *runtimesettings.Controller
	logger         *slog.Logger
	backupSchedule func(context.Context)
}

func (r runtimeSettingsRefresher) Run(ctx context.Context) error {
	return sampleLoop(ctx, time.Second, func(ctx context.Context) {
		refreshCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if err := r.settings.Refresh(refreshCtx); err != nil {
			if ctx.Err() == nil {
				r.logger.ErrorContext(ctx, "refresh runtime settings", "error", err)
			}
			return
		}
		if r.backupSchedule != nil {
			r.backupSchedule(refreshCtx)
		}
	})
}

func bindRuntimeSettingsLifecycle(app *App, settings *runtimesettings.Controller) {
	previous := settings.Get()
	// Controller serializes publication and calls subscribers only after a
	// successful commit or reload. The hook observes the effective snapshot.
	settings.Subscribe(func(current runtimesettings.Settings) {
		before := []any{previous.Batch, previous.Logs, previous.Proxy, previous.Backups, previous.Application, previous.RateLimits}
		after := []any{current.Batch, current.Logs, current.Proxy, current.Backups, current.Application, current.RateLimits}
		names := []string{"batch", "logs", "proxy", "backups", "application", "rateLimits"}
		changed := make([]string, 0, len(names))
		for i, name := range names {
			if !reflect.DeepEqual(before[i], after[i]) {
				changed = append(changed, name)
			}
		}
		previous = current
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		app.NotifySettingsReload(ctx, changed)
	})
}
