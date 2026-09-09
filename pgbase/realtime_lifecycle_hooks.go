package pgbase

import (
	"context"

	"github.com/webisnoteasy/PgBase/internal/api"
)

const (
	HookRealtimeConnect   = "realtime.connect"
	HookRealtimeSubscribe = "realtime.subscribe"
	HookRealtimeMessage   = "realtime.message"
)

type RealtimeEvent = api.RealtimeLifecycleEvent

type realtimeLifecycleHooks struct{ registry *HookRegistry }

func (h realtimeLifecycleHooks) begin(ctx context.Context, kind string, event *api.RealtimeLifecycleEvent) (func(error), error) {
	return h.registry.Begin(ctx, &HookEvent{Kind: kind, Data: event})
}
func (h realtimeLifecycleHooks) BeginRealtimeConnect(ctx context.Context, event *api.RealtimeLifecycleEvent) (func(error), error) {
	return h.begin(ctx, HookRealtimeConnect, event)
}
func (h realtimeLifecycleHooks) BeginRealtimeSubscribe(ctx context.Context, event *api.RealtimeLifecycleEvent) (func(error), error) {
	return h.begin(ctx, HookRealtimeSubscribe, event)
}
func (h realtimeLifecycleHooks) BeginRealtimeMessage(ctx context.Context, event *api.RealtimeLifecycleEvent) (func(error), error) {
	return h.begin(ctx, HookRealtimeMessage, event)
}
