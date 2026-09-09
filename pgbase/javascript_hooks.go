package pgbase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/webisnoteasy/PgBase/internal/api"
	"github.com/webisnoteasy/PgBase/internal/jsvm"
	"github.com/webisnoteasy/PgBase/internal/lifecycle"
	"github.com/webisnoteasy/PgBase/internal/mailer"
)

const maximumJavaScriptHookState = 8 << 10

type javaScriptHookInvocation struct {
	Event  string          `json:"event"`
	Phase  string          `json:"phase"`
	Data   json.RawMessage `json:"data"`
	Failed bool            `json:"failed"`
	State  json.RawMessage `json:"state,omitempty"`
}

type javaScriptHookResult struct {
	Patch json.RawMessage `json:"patch,omitempty"`
	State json.RawMessage `json:"state,omitempty"`
}

type javaScriptLifecycleHook struct {
	runtime      *javaScriptRuntime
	descriptor   jsvm.Hook
	mu           sync.Mutex
	stateByEvent map[*HookEvent]json.RawMessage
}

func (runtime *javaScriptRuntime) installHooks(registry *HookRegistry) error {
	if runtime == nil || len(runtime.bundle.Manifest().Hooks) == 0 {
		return nil
	}
	if registry == nil {
		return errors.New("JavaScript hooks require a hook registry")
	}
	for _, descriptor := range runtime.bundle.Manifest().Hooks {
		adapter := &javaScriptLifecycleHook{runtime: runtime, descriptor: descriptor, stateByEvent: map[*HookEvent]json.RawMessage{}}
		hook := Hook{Priority: descriptor.Priority}
		if descriptor.Before != nil {
			hook.Before = adapter.before
		}
		if descriptor.After != nil {
			hook.After = adapter.after
		}
		remove, err := registry.On(descriptor.Event, hook)
		if err != nil {
			runtime.removeHooks()
			return fmt.Errorf("register JavaScript hook %s: %w", descriptor.Event, err)
		}
		runtime.hookRemovers = append(runtime.hookRemovers, remove)
	}
	return nil
}

func (runtime *javaScriptRuntime) removeHooks() {
	for index := len(runtime.hookRemovers) - 1; index >= 0; index-- {
		runtime.hookRemovers[index]()
	}
	runtime.hookRemovers = nil
}

func (hook *javaScriptLifecycleHook) before(ctx context.Context, event *HookEvent) error {
	result, err := hook.invoke(ctx, event, "before", *hook.descriptor.Before, nil)
	if len(result.State) > maximumJavaScriptHookState {
		return errors.New("JavaScript hook state exceeds 8192 bytes")
	}
	if hook.descriptor.After != nil && len(result.State) != 0 {
		hook.mu.Lock()
		hook.stateByEvent[event] = append(json.RawMessage(nil), result.State...)
		hook.mu.Unlock()
	}
	if err != nil {
		return err
	}
	return applyJavaScriptHookPatch(event, result.Patch)
}

func (hook *javaScriptLifecycleHook) after(ctx context.Context, event *HookEvent) {
	hook.mu.Lock()
	state := hook.stateByEvent[event]
	delete(hook.stateByEvent, event)
	hook.mu.Unlock()
	_, _ = hook.invoke(ctx, event, "after", *hook.descriptor.After, state)
}

func (hook *javaScriptLifecycleHook) invoke(ctx context.Context, event *HookEvent, phase string, handler jsvm.HandlerRef, state json.RawMessage) (javaScriptHookResult, error) {
	data, err := safeJavaScriptHookData(event)
	if err != nil {
		return javaScriptHookResult{}, err
	}
	input, err := json.Marshal(javaScriptHookInvocation{Event: event.Kind, Phase: phase, Data: data, Failed: event.Err != nil, State: state})
	if err != nil {
		return javaScriptHookResult{}, err
	}
	output, err := hook.runtime.engine.Invoke(ctx, jsvm.Invocation{Handler: handler, Capabilities: hook.descriptor.Capabilities,
		Input: input, Host: hook.runtime.bridge.forHook(ctx, event.Kind)})
	if err != nil {
		return javaScriptHookResult{}, err
	}
	var result javaScriptHookResult
	if err := decodeStrictJSON(output, &result); err != nil {
		return javaScriptHookResult{}, fmt.Errorf("JavaScript hook %s returned invalid result: %w", event.Kind, err)
	}
	return result, nil
}

func safeJavaScriptHookData(event *HookEvent) (json.RawMessage, error) {
	if event == nil {
		return nil, errors.New("JavaScript hook event is nil")
	}
	if request, ok := event.Data.(*RequestEvent); ok {
		principal, _ := PrincipalFromContext(request.Request.Context())
		return json.Marshal(map[string]any{"method": request.Request.Method, "path": request.Request.URL.Path,
			"query": cloneQuery(request.Request.URL.Query()), "headers": safeJavaScriptHeaders(request.Request.Header), "principal": principal})
	}
	if message, ok := event.Data.(*mailer.Message); ok {
		return json.Marshal(extensionMailRequest{To: message.To, Subject: message.Subject, Text: message.Text, HTML: message.HTML})
	}
	if mailEvent, ok := event.Data.(*MailEvent); ok {
		return json.Marshal(map[string]any{"purpose": mailEvent.Purpose, "message": extensionMailRequest{
			To: mailEvent.Message.To, Subject: mailEvent.Message.Subject, Text: mailEvent.Message.Text, HTML: mailEvent.Message.HTML}})
	}
	raw, err := json.Marshal(event.Data)
	if err != nil {
		return nil, fmt.Errorf("encode JavaScript hook %s data: %w", event.Kind, err)
	}
	return raw, nil
}

func applyJavaScriptHookPatch(event *HookEvent, raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
		return nil
	}
	switch data := event.Data.(type) {
	case *mailer.Message:
		var patch extensionMailRequest
		if err := decodeStrictJSON(raw, &patch); err != nil {
			return invalidJavaScriptPatch(event.Kind, err)
		}
		purpose := data.Purpose
		data.To, data.Subject, data.Text, data.HTML = patch.To, patch.Subject, patch.Text, patch.HTML
		data.Purpose = purpose
	case *MailEvent:
		var patch struct {
			Message extensionMailRequest `json:"message"`
		}
		if err := decodeStrictJSON(raw, &patch); err != nil {
			return invalidJavaScriptPatch(event.Kind, err)
		}
		purpose := data.Message.Purpose
		data.Message.To, data.Message.Subject, data.Message.Text, data.Message.HTML = patch.Message.To, patch.Message.Subject, patch.Message.Text, patch.Message.HTML
		data.Message.Purpose = purpose
	case *api.RecordRequestEvent:
		var patch struct {
			Limit  *int                       `json:"limit,omitempty"`
			Cursor *string                    `json:"cursor,omitempty"`
			Filter *string                    `json:"filter,omitempty"`
			Sort   *string                    `json:"sort,omitempty"`
			Expand *string                    `json:"expand,omitempty"`
			Input  map[string]json.RawMessage `json:"input,omitempty"`
		}
		if err := decodeStrictJSON(raw, &patch); err != nil {
			return invalidJavaScriptPatch(event.Kind, err)
		}
		if patch.Limit != nil {
			data.Limit = *patch.Limit
		}
		if patch.Cursor != nil {
			data.Cursor = *patch.Cursor
		}
		if patch.Filter != nil {
			data.Filter = *patch.Filter
		}
		if patch.Sort != nil {
			data.Sort = *patch.Sort
		}
		if patch.Expand != nil {
			data.Expand = *patch.Expand
		}
		if patch.Input != nil {
			data.Input = cloneRawFields(patch.Input)
		}
	case *RequestEvent:
		var patch struct {
			Headers map[string]string `json:"headers"`
		}
		if err := decodeStrictJSON(raw, &patch); err != nil {
			return invalidJavaScriptPatch(event.Kind, err)
		}
		for name, value := range patch.Headers {
			if !allowedJavaScriptRequestPatchHeader(name) || strings.ContainsAny(value, "\r\n") {
				return invalidJavaScriptPatch(event.Kind, errors.New("request header is not mutable"))
			}
			data.Request.Header.Set(name, value)
		}
	default:
		return invalidJavaScriptPatch(event.Kind, errors.New("event is read-only"))
	}
	return nil
}

func allowedJavaScriptRequestPatchHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Accept", "Content-Type", "If-Match", "Idempotency-Key", "X-Extension":
		return true
	default:
		return false
	}
}

func decodeStrictJSON(raw []byte, destination any) error {
	return decodeExtensionInput(raw, destination)
}

func invalidJavaScriptPatch(event string, cause error) error {
	return fmt.Errorf("JavaScript hook %s invalid patch: %w", event, cause)
}

var _ = lifecycle.Event{}
