package jsvm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/require"
)

const registeredExportPrefix = "$registered:"

type registrationSet struct {
	handlers         map[string]goja.Callable
	handlerValues    map[string]goja.Value
	closed           bool
	ordinal          int
	globalMiddleware []goja.Value
}

func registrationID(module, kind, name string, ordinal int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d", module, kind, name, ordinal)))
	return registeredExportPrefix + hex.EncodeToString(sum[:16])
}

func (r *Runtime) collectBootstrap() error {
	if r.bundle.manifest.Bootstrap == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(r.root, 2*time.Second)
	defer cancel()
	vm := goja.New()
	state := &callState{ctx: ctx, capabilities: map[string]bool{}, hostCalls: &atomic.Int64{}, limits: r.limits}
	r.states.Store(vm, state)
	defer r.states.Delete(vm)
	requireModule := r.registry.Enable(vm)
	if err := r.installPocketBaseGlobals(vm, state); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			vm.Interrupt(ctx.Err())
		case <-done:
		}
	}()
	defer close(done)
	set, err := r.installRegistrationGlobals(vm, true)
	if err != nil {
		return err
	}
	module, _ := r.bundle.canonicalModule(r.bundle.manifest.Bootstrap)
	if _, err = requireModule.Require(virtualRoot + "/" + module); err != nil {
		return fmt.Errorf("execute JavaScript bootstrap: %w", err)
	}
	set.closed = true
	for _, route := range r.bundle.manifest.Routes {
		if strings.HasPrefix(route.Handler.Export, registeredExportPrefix) && set.handlers[route.Handler.Export] == nil {
			return fmt.Errorf("bootstrap route %s %s was not registered", route.Method, route.Pattern)
		}
	}
	for _, job := range r.bundle.manifest.Jobs {
		if strings.HasPrefix(job.Handler.Export, registeredExportPrefix) && set.handlers[job.Handler.Export] == nil {
			return fmt.Errorf("bootstrap job %s was not registered", job.Name)
		}
	}
	return nil
}

func (r *Runtime) installRegistrationGlobals(vm *goja.Runtime, collecting bool) (*registrationSet, error) {
	set := &registrationSet{handlers: map[string]goja.Callable{}, handlerValues: map[string]goja.Value{}}
	register := func(kind, name string, value goja.Value, middlewares []goja.Value) (string, error) {
		if set.closed {
			return "", errors.New("registration_closed")
		}
		callable, ok := goja.AssertFunction(value)
		if !ok {
			return "", errors.New("registered handler must be a function")
		}
		all := append([]goja.Value{}, middlewares...)
		if kind == "route" {
			all = append(append([]goja.Value{}, set.globalMiddleware...), all...)
		}
		handlerValue := value
		for i := len(all) - 1; i >= 0; i-- {
			mw, ok := goja.AssertFunction(all[i])
			if !ok {
				return "", errors.New("middleware must be a function")
			}
			inner := callable
			next := vm.ToValue(func(call goja.FunctionCall) goja.Value {
				value, callErr := inner(goja.Undefined(), call.Arguments...)
				if callErr != nil {
					panic(callErr)
				}
				return value
			})
			wrapped, err := mw(goja.Undefined(), next)
			if err != nil {
				return "", err
			}
			callable, ok = goja.AssertFunction(wrapped)
			if !ok {
				return "", errors.New("middleware must return a function")
			}
			handlerValue = wrapped
		}
		id := registrationID(r.bundle.manifest.Bootstrap, kind, name, set.ordinal)
		set.ordinal++
		set.handlers[id] = callable
		set.handlerValues[id] = handlerValue
		return id, nil
	}
	if err := vm.Set("routerUse", func(values ...goja.Value) error {
		if set.closed {
			return errors.New("registration_closed")
		}
		set.globalMiddleware = append(set.globalMiddleware, values...)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := vm.Set("routerAdd", func(method, path string, handler goja.Value, middlewares ...goja.Value) error {
		id, err := register("route", strings.ToUpper(method)+" "+path, handler, middlewares)
		if err != nil {
			return err
		}
		if collecting {
			found := false
			for i := range r.bundle.manifest.Routes {
				route := &r.bundle.manifest.Routes[i]
				if strings.EqualFold(route.Method, method) && route.Pattern == path {
					route.Handler = HandlerRef{Module: r.bundle.manifest.Bootstrap, Export: id}
					found = true
					break
				}
			}
			if !found {
				return errors.New("routerAdd is not predeclared by operator manifest")
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := vm.Set("cronAdd", func(name, schedule string, handler goja.Value) error {
		id, err := register("job", name, handler, nil)
		if err != nil {
			return err
		}
		if collecting {
			found := false
			for i := range r.bundle.manifest.Jobs {
				job := &r.bundle.manifest.Jobs[i]
				if job.Name == name && job.Schedule == schedule {
					job.Handler = HandlerRef{Module: r.bundle.manifest.Bootstrap, Export: id}
					found = true
					break
				}
			}
			if !found {
				return errors.New("cronAdd is not predeclared by operator manifest")
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := vm.Set("cronRemove", func(name string) error {
		if set.closed {
			return errors.New("registration_closed")
		}
		if collecting {
			for _, job := range r.bundle.manifest.Jobs {
				if job.Name == name {
					return errors.New("cronRemove cannot remove an operator-declared job")
				}
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	type hookSlot struct {
		index  int
		before bool
	}
	hookSlots := map[string][]hookSlot{}
	for i, hook := range r.bundle.manifest.Hooks {
		if hook.Before != nil {
			hookSlots[hook.Event] = append(hookSlots[hook.Event], hookSlot{i, true})
		}
		if hook.After != nil {
			hookSlots[hook.Event] = append(hookSlots[hook.Event], hookSlot{i, false})
		}
	}
	for event, slots := range hookSlots {
		event, slots := event, slots
		used := 0
		registerHook := func(handlerValue goja.Value, tags ...string) error {
			handler, ok := goja.AssertFunction(handlerValue)
			if !ok {
				return errors.New("registered handler must be a function")
			}
			registeredValue := vm.ToValue(func(inner goja.FunctionCall) goja.Value {
				if len(inner.Arguments) == 0 {
					panic(vm.NewTypeError("hook event is required"))
				}
				obj := inner.Arguments[0].ToObject(vm)
				if len(tags) != 0 && !hookTagMatches(vm, obj, tags) {
					return vm.ToValue(map[string]any{})
				}
				called := false
				if err := obj.Set("next", func() { called = true }); err != nil {
					panic(vm.NewGoError(err))
				}
				result, err := handler(goja.Undefined(), obj)
				if err != nil {
					panic(err)
				}
				// Existing PgBase handlers predate e.next and are terminal JSON
				// transformers. Calling next is accepted; thrown errors still veto.
				_ = called
				return result
			})
			id, err := register("hook", event, registeredValue, nil)
			if err != nil {
				return err
			}
			if collecting {
				if used >= len(slots) {
					return errors.New("hook registration is not predeclared by operator manifest")
				}
				slot := slots[used]
				used++
				hook := &r.bundle.manifest.Hooks[slot.index]
				ref := HandlerRef{Module: r.bundle.manifest.Bootstrap, Export: id}
				if slot.before {
					hook.Before = &ref
				} else {
					hook.After = &ref
				}
			}
			return nil
		}
		for _, global := range hookGlobalNames(event) {
			if err := vm.Set(global, registerHook); err != nil {
				return nil, err
			}
		}
	}
	return set, nil
}

func hookTagMatches(vm *goja.Runtime, event *goja.Object, tags []string) bool {
	data := event.Get("data")
	if data == nil || goja.IsUndefined(data) || goja.IsNull(data) {
		return false
	}
	obj := data.ToObject(vm)
	if nested := obj.Get("model"); nested != nil && !goja.IsUndefined(nested) && !goja.IsNull(nested) {
		obj = nested.ToObject(vm)
	}
	for _, key := range []string{"collection", "collectionName", "name", "realm"} {
		value := obj.Get(key)
		if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
			continue
		}
		for _, tag := range tags {
			if value.String() == tag {
				return true
			}
		}
	}
	return false
}

func hookGlobalName(event string) string {
	var b strings.Builder
	b.WriteString("on")
	upper := true
	for _, r := range event {
		if r == '.' || r == '_' || r == '-' {
			upper = true
			continue
		}
		if upper {
			r = unicode.ToUpper(r)
			upper = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

func hookGlobalNames(event string) []string {
	aliases := map[string]string{"mail.send": "onMailerSend", "mail.otp": "onMailerRecordOTPSend", "mail.verification": "onMailerRecordVerificationSend", "mail.password_reset": "onMailerRecordPasswordResetSend", "mail.email_change": "onMailerRecordEmailChangeSend", "record.list": "onRecordsListRequest", "record.view": "onRecordViewRequest", "record.create": "onRecordCreateRequest", "record.update": "onRecordUpdateRequest", "record.delete": "onRecordDeleteRequest", "record.batch": "onBatchRequest", "file.download": "onFileDownloadRequest", "file.token": "onFileTokenRequest", "settings.list": "onSettingsListRequest", "settings.update": "onSettingsUpdateRequest", "settings.reload": "onSettingsReload", "realtime.connect": "onRealtimeConnectRequest", "realtime.subscribe": "onRealtimeSubscribeRequest", "realtime.message": "onRealtimeMessageSend", "backup.create": "onBackupCreate", "backup.restore": "onBackupRestore", "app.boot": "onBootstrap", "app.serve": "onServe", "app.terminate": "onTerminate"}
	if event == "schema.import.preview" || event == "schema.import.apply" {
		return []string{"onCollectionsImportRequest", hookGlobalName(event)}
	}
	phaseAliases := map[string]string{
		"model.validate": "onModelValidate", "model.create": "onModelCreate", "model.create.execute": "onModelCreateExecute", "model.create.success": "onModelAfterCreateSuccess", "model.create.error": "onModelAfterCreateError",
		"model.update": "onModelUpdate", "model.update.execute": "onModelUpdateExecute", "model.update.success": "onModelAfterUpdateSuccess", "model.update.error": "onModelAfterUpdateError",
		"model.delete": "onModelDelete", "model.delete.execute": "onModelDeleteExecute", "model.delete.success": "onModelAfterDeleteSuccess", "model.delete.error": "onModelAfterDeleteError",
		"record.model.validate": "onRecordValidate", "record.model.create": "onRecordCreate", "record.model.create.execute": "onRecordCreateExecute", "record.model.create.success": "onRecordAfterCreateSuccess", "record.model.create.error": "onRecordAfterCreateError",
		"record.model.update": "onRecordUpdate", "record.model.update.execute": "onRecordUpdateExecute", "record.model.update.success": "onRecordAfterUpdateSuccess", "record.model.update.error": "onRecordAfterUpdateError",
		"record.model.delete": "onRecordDelete", "record.model.delete.execute": "onRecordDeleteExecute", "record.model.delete.success": "onRecordAfterDeleteSuccess", "record.model.delete.error": "onRecordAfterDeleteError",
		"collection.model.validate": "onCollectionValidate", "collection.model.create": "onCollectionCreate", "collection.model.create.execute": "onCollectionCreateExecute", "collection.model.create.success": "onCollectionAfterCreateSuccess", "collection.model.create.error": "onCollectionAfterCreateError",
		"collection.model.update": "onCollectionUpdate", "collection.model.update.execute": "onCollectionUpdateExecute", "collection.model.update.success": "onCollectionAfterUpdateSuccess", "collection.model.update.error": "onCollectionAfterUpdateError",
		"collection.model.delete": "onCollectionDelete", "collection.model.delete.execute": "onCollectionDeleteExecute", "collection.model.delete.success": "onCollectionAfterDeleteSuccess", "collection.model.delete.error": "onCollectionAfterDeleteError",
	}
	if alias := phaseAliases[event]; alias != "" {
		return []string{alias, hookGlobalName(event)}
	}
	base := hookGlobalName(event)
	if event == "auth.refresh" {
		return []string{"onRecordAuthRequest", "onRecordAuthRefreshRequest", base}
	}
	if event == "auth.password" {
		return []string{"onRecordAuthRequest", "onRecordAuthWithPasswordRequest", base}
	}
	if event == "auth.oauth.begin" || event == "auth.oauth.complete" {
		return []string{"onRecordAuthRequest", "onRecordAuthWithOAuth2Request", base}
	}
	if event == "auth.otp.request" {
		return []string{"onRecordRequestOTPRequest", base}
	}
	if event == "auth.otp.complete" {
		return []string{"onRecordAuthRequest", "onRecordAuthWithOTPRequest", base}
	}
	if event == "auth.password_reset.request" {
		return []string{"onRecordRequestPasswordResetRequest", base}
	}
	if event == "auth.password_reset.confirm" {
		return []string{"onRecordConfirmPasswordResetRequest", base}
	}
	if event == "auth.verification.request" {
		return []string{"onRecordRequestVerificationRequest", base}
	}
	if event == "auth.verification.confirm" {
		return []string{"onRecordConfirmVerificationRequest", base}
	}
	if event == "auth.email_change.request" {
		return []string{"onRecordRequestEmailChangeRequest", base}
	}
	if event == "auth.email_change.confirm" {
		return []string{"onRecordConfirmEmailChangeRequest", base}
	}
	if event == "auth.alert" {
		return []string{"onMailerRecordAuthAlertSend", base}
	}
	if alias := aliases[event]; alias != "" && alias != base {
		return []string{alias, base}
	}
	return []string{base}
}

func (r *Runtime) replayRegistrations(vm *goja.Runtime, requireModule *require.RequireModule) (*registrationSet, error) {
	set, err := r.installRegistrationGlobals(vm, false)
	if err != nil {
		return nil, err
	}
	module, _ := r.bundle.canonicalModule(r.bundle.manifest.Bootstrap)
	if _, err = requireModule.Require(virtualRoot + "/" + module); err != nil {
		return nil, err
	}
	set.closed = true
	return set, nil
}
