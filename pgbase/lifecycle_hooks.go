package pgbase

import (
	"context"
	"errors"
	"net/http"

	"github.com/yakisoba0728/PgBase/internal/api"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/lifecycle"
	"github.com/yakisoba0728/PgBase/internal/mailer"
	"github.com/yakisoba0728/PgBase/internal/recordops"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// HookRegistry supports runtime registration/removal without changing in-flight
// operations. Lower Priority runs first; equal priorities use registration order.
type HookRegistry = lifecycle.Registry
type Hook = lifecycle.Hook
type HookEvent = lifecycle.Event

const (
	HookRequest                  = "request"
	HookRecordBefore             = "record.before"
	HookRecordAfter              = "record.after"
	HookMailSend                 = "mail.send"
	HookMailOTP                  = "mail.otp"
	HookMailVerification         = "mail.verification"
	HookMailPasswordReset        = "mail.password_reset"
	HookMailEmailChange          = "mail.email_change"
	HookMailSMTPTest             = "mail.smtp_test"
	HookFileUpload               = "file.upload"
	HookFileDelete               = "file.delete"
	HookFileToken                = "file.token"
	HookFileDownload             = "file.download"
	HookFileTransform            = "file.transform"
	HookRecordListRequest        = "record.list"
	HookRecordViewRequest        = "record.view"
	HookRecordCreateRequest      = "record.create"
	HookRecordUpdateRequest      = "record.update"
	HookRecordDeleteRequest      = "record.delete"
	HookRecordBatchRequest       = "record.batch"
	HookSchemaImportPreview      = "schema.import.preview"
	HookSchemaImportApply        = "schema.import.apply"
	HookCollectionCreate         = "collection.create"
	HookCollectionUpdate         = "collection.update"
	HookCollectionDelete         = "collection.delete"
	HookAppBoot                  = "app.boot"
	HookAppServe                 = "app.serve"
	HookAppTerminate             = "app.terminate"
	HookAuthPassword             = "auth.password"
	HookAuthRefresh              = "auth.refresh"
	HookAuthOAuthBegin           = "auth.oauth.begin"
	HookAuthOAuthComplete        = "auth.oauth.complete"
	HookAuthOTPRequest           = "auth.otp.request"
	HookAuthOTPComplete          = "auth.otp.complete"
	HookAuthVerificationRequest  = "auth.verification.request"
	HookAuthVerificationConfirm  = "auth.verification.confirm"
	HookAuthPasswordResetRequest = "auth.password_reset.request"
	HookAuthPasswordResetConfirm = "auth.password_reset.confirm"
	HookAuthEmailChangeRequest   = "auth.email_change.request"
	HookAuthEmailChangeConfirm   = "auth.email_change.confirm"
	HookAuthImpersonate          = "auth.impersonate"
	HookAuthAlert                = "auth.alert"
	HookSettingsReload           = "settings.reload"
	HookObjectWrite              = "object.write"
	HookObjectDelete             = "object.delete"
)

type CollectionEvent = schema.CollectionMutationEvent

type schemaLifecycleHooks struct{ registry *HookRegistry }

type skipJavaScriptSchemaHooksKey struct{}

func (h schemaLifecycleHooks) BeginCollectionMutation(ctx context.Context, event *schema.CollectionMutationEvent) (func(error), error) {
	if skip, _ := ctx.Value(skipJavaScriptSchemaHooksKey{}).(bool); skip {
		return func(error) {}, nil
	}
	kind := HookCollectionCreate
	switch event.Operation {
	case "update":
		kind = HookCollectionUpdate
	case "delete":
		kind = HookCollectionDelete
	}
	return h.registry.Begin(ctx, &HookEvent{Kind: kind, Data: event})
}

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
type FileAccessEvent = filestore.FileAccessEvent

// RecordRequestEvent and BatchRequestEvent are typed request payloads. Their
// documented mutable fields are passed to the service; identity fields are
// copied observations and cannot alter authorization or idempotency.
type RecordRequestEvent = api.RecordRequestEvent
type BatchRequestEvent = api.BatchRequestEvent
type SchemaImportRequestEvent = api.SchemaImportRequestEvent

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
	// Preserve the established mail.send payload. Purpose-specific hooks receive
	// the richer MailEvent after generic hooks have adjusted the message.
	purpose := message.Purpose
	finish, err := m.registry.Begin(ctx, &HookEvent{Kind: HookMailSend, Data: &message})
	if err != nil {
		finish(err)
		return err
	}
	// Purpose belongs to the trusted calling service. A generic mail hook may
	// edit deliverable fields but cannot recategorize a send into another hook.
	message.Purpose = purpose
	if kind := mailPurposeHook(message.Purpose); kind != "" {
		event := &MailEvent{Purpose: message.Purpose, Message: message}
		purposeFinish, purposeErr := m.registry.Begin(ctx, &HookEvent{Kind: kind, Data: event})
		if purposeErr != nil {
			purposeFinish(purposeErr)
			finish(purposeErr)
			return purposeErr
		}
		// The purpose is chosen by the service, not an extension. Restore it
		// after purpose-specific mutation as well as after the generic hook.
		event.Message.Purpose = purpose
		err = m.sender.Send(ctx, event.Message)
		message = event.Message
		message.Purpose = purpose
		purposeFinish(err)
		finish(err)
		return err
	}
	err = m.sender.Send(ctx, message)
	finish(err)
	return err
}

// MailEvent deliberately has no SMTP configuration or credentials. Message
// content can be adjusted before delivery, while Purpose identifies the
// trusted service path and is observational.
type MailEvent struct {
	Purpose string
	Message mailer.Message
}

func mailPurposeHook(purpose string) string {
	switch purpose {
	case "otp":
		return HookMailOTP
	case "verification":
		return HookMailVerification
	case "password_reset":
		return HookMailPasswordReset
	case "email_change":
		return HookMailEmailChange
	case "smtp_test":
		return HookMailSMTPTest
	case "auth_alert":
		return HookAuthAlert
	default:
		return ""
	}
}

// SettingsReloadEvent is emitted after durable settings are committed and the
// live snapshot is replaced. Changed contains stable setting names, not secrets.
type SettingsReloadEvent struct{ Changed []string }

// NotifySettingsReload lets the settings service publish its commit boundary
// without exposing settings storage to extensions.
func (a *App) NotifySettingsReload(ctx context.Context, changed []string) {
	if a == nil || a.options.Hooks == nil {
		return
	}
	copyOfChanged := append([]string(nil), changed...)
	_ = a.options.Hooks.Run(ctx, &HookEvent{Kind: HookSettingsReload, Data: &SettingsReloadEvent{Changed: copyOfChanged}}, func() error { return nil })
}

func applicationMutationHooks(options Options) []MutationHook {
	hooks := append([]MutationHook(nil), options.MutationHooks...)
	if options.Hooks != nil {
		hooks = append(hooks, recordLifecycleHooks{options.Hooks})
	}
	return hooks
}
