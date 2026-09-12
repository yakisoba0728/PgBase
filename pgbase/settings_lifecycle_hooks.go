package pgbase

import (
	"context"

	"github.com/yakisoba0728/PgBase/internal/api"
)

type settingsLifecycleHooks struct{ registry *HookRegistry }

func (h settingsLifecycleHooks) begin(ctx context.Context, kind string, event *api.SettingsLifecycleEvent) (func(error), error) {
	return h.registry.Begin(ctx, &HookEvent{Kind: kind, Data: event})
}

func (h settingsLifecycleHooks) BeginSettingsList(ctx context.Context, event *api.SettingsLifecycleEvent) (func(error), error) {
	return h.begin(ctx, HookSettingsList, event)
}

func (h settingsLifecycleHooks) BeginSettingsUpdate(ctx context.Context, event *api.SettingsLifecycleEvent) (func(error), error) {
	return h.begin(ctx, HookSettingsUpdate, event)
}
