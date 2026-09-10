package pgbase

import (
	"context"
	"reflect"
	"time"

	"github.com/yakisoba0728/PgBase/internal/runtimesettings"
)

func bindRuntimeSettingsLifecycle(app *App, settings *runtimesettings.Controller) {
	previous := settings.Get()
	// Controller serializes publication and calls subscribers only after a
	// successful commit or reload. The hook observes the effective snapshot.
	settings.Subscribe(func(current runtimesettings.Settings) {
		before := []any{previous.Batch, previous.Logs, previous.Proxy, previous.Backups, previous.Application}
		after := []any{current.Batch, current.Logs, current.Proxy, current.Backups, current.Application}
		names := []string{"batch", "logs", "proxy", "backups", "application"}
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
