package pgbase

import (
	"context"
	"errors"
	"net/http"

	"github.com/webisnoteasy/PgBase/internal/filestore"
	"github.com/webisnoteasy/PgBase/internal/lifecycle"
	"github.com/webisnoteasy/PgBase/internal/mailer"
	"github.com/webisnoteasy/PgBase/internal/recordops"
)

// HookRegistry supports runtime registration/removal without changing in-flight
// operations. Lower Priority runs first; equal priorities use registration order.
type HookRegistry = lifecycle.Registry
type Hook = lifecycle.Hook
type HookEvent = lifecycle.Event

const (
	HookRequest      = "request"
	HookRecordBefore = "record.before"
	HookRecordAfter  = "record.after"
	HookMailSend     = "mail.send"
	HookFileUpload   = "file.upload"
	HookFileDelete   = "file.delete"
)

// RequestEvent gives trusted extensions the request and response writer before
// routing. Before may adjust headers/context. After observes completed response;
// it must not write headers or body. Returning an error in Before rejects with 500.
type RequestEvent struct {
	Request  *http.Request
	Response http.ResponseWriter
}

// MailMessage can be adjusted in mail.send Before, before transport validation.
type MailMessage = mailer.Message

// FileEvent describes file metadata without retaining uploaded bytes.
type FileEvent = filestore.FileEvent

func requestHooks(registry *HookRegistry, next http.Handler) http.Handler {
	if registry == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		event := &HookEvent{Kind: HookRequest, Data: &RequestEvent{Request: r, Response: w}}
		finish, err := registry.Begin(r.Context(), event)
		if err != nil {
			http.Error(w, "request extension rejected operation", http.StatusInternalServerError)
			finish(err)
			return
		}
		defer func() {
			if cause := recover(); cause != nil {
				finish(errors.New("request operation panicked"))
				panic(cause)
			}
			finish(nil)
		}()
		payload := event.Data.(*RequestEvent)
		next.ServeHTTP(payload.Response, payload.Request)
	})
}

type recordLifecycleHooks struct{ registry *HookRegistry }

func (h recordLifecycleHooks) BeforeMutation(ctx context.Context, mutation recordops.Mutation) error {
	// Validation is inside the write transaction and can repeat on retry. Do not
	// perform external side effects here; record.after is commit-only observation.
	return h.registry.Run(ctx, &HookEvent{Kind: HookRecordBefore, Data: mutation}, func() error { return nil })
}
func (h recordLifecycleHooks) AfterMutation(ctx context.Context, mutation recordops.Mutation) {
	_ = h.registry.Run(ctx, &HookEvent{Kind: HookRecordAfter, Data: mutation}, func() error { return nil })
}

type hookedMailer struct {
	registry *HookRegistry
	sender   mailer.Sender
}

func (m hookedMailer) Send(ctx context.Context, message mailer.Message) error {
	return m.registry.Run(ctx, &HookEvent{Kind: HookMailSend, Data: &message}, func() error { return m.sender.Send(ctx, message) })
}

func applicationMutationHooks(options Options) []MutationHook {
	hooks := append([]MutationHook(nil), options.MutationHooks...)
	if options.Hooks != nil {
		hooks = append(hooks, recordLifecycleHooks{options.Hooks})
	}
	return hooks
}
