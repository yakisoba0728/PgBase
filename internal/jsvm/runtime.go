package jsvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja/parser"
	"github.com/dop251/goja_nodejs/require"
)

type ErrorCode string

const (
	CodeBusy             ErrorCode = "js_busy"
	CodeTimeout          ErrorCode = "js_timeout"
	CodeCanceled         ErrorCode = "js_canceled"
	CodeLimitExceeded    ErrorCode = "js_limit_exceeded"
	CodeExecutionFailed  ErrorCode = "js_execution_failed"
	CodeInvalidResult    ErrorCode = "js_invalid_result"
	CodeCapabilityDenied ErrorCode = "js_capability_denied"
	CodeRuntimeClosed    ErrorCode = "js_runtime_closed"
)

type Error struct {
	Code        ErrorCode
	PublicError *PublicAPIError
	cause       error
}

// PublicAPIError is a validated compatibility ApiError that may be returned to
// an HTTP client. Other JavaScript exceptions intentionally remain opaque.
type PublicAPIError struct {
	Status  int
	Message string
	Data    json.RawMessage
}

func (e *Error) Error() string {
	if e == nil || e.Code == "" {
		return string(CodeExecutionFailed)
	}
	return string(e.Code)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

type Host interface {
	Call(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

// InputPreparer lets a transport host defer request-body consumption until the
// exported handler has been loaded. It is intentionally optional so jobs and
// hooks retain their existing JSON-only invocation contract.
type InputPreparer interface {
	PrepareInput(context.Context, json.RawMessage, int64) (json.RawMessage, error)
}

type inputTooLarge interface{ InputTooLarge() bool }

type Invocation struct {
	Handler      HandlerRef
	Capabilities []string
	Input        json.RawMessage
	Host         Host
	Timeout      time.Duration
	// OnSkipSuccessActivityLog is invoked after a route handler has been
	// resolved and is marked by $apis.skipSuccessActivityLog. The callback is
	// supplied by the HTTP transport, never by script input.
	OnSkipSuccessActivityLog func()
}

type Limits struct {
	Concurrency        int
	Queue              int
	AdmissionTimeout   time.Duration
	DefaultTimeout     time.Duration
	MaximumTimeout     time.Duration
	MaximumHostCalls   int
	MaximumInputBytes  int
	MaximumOutputBytes int
	MaximumJSONDepth   int
	MaximumCallStack   int
}

func (limits Limits) normalized() (Limits, error) {
	if limits.Concurrency == 0 {
		limits.Concurrency = 8
	}
	if limits.Queue == 0 {
		limits.Queue = 32
	}
	if limits.AdmissionTimeout == 0 {
		limits.AdmissionTimeout = 250 * time.Millisecond
	}
	if limits.DefaultTimeout == 0 {
		limits.DefaultTimeout = 5 * time.Second
	}
	if limits.MaximumTimeout == 0 {
		limits.MaximumTimeout = 30 * time.Second
	}
	if limits.MaximumHostCalls == 0 {
		limits.MaximumHostCalls = 256
	}
	if limits.MaximumInputBytes == 0 {
		limits.MaximumInputBytes = 1 << 20
	}
	if limits.MaximumOutputBytes == 0 {
		limits.MaximumOutputBytes = 4 << 20
	}
	if limits.MaximumJSONDepth == 0 {
		limits.MaximumJSONDepth = 64
	}
	if limits.MaximumCallStack == 0 {
		limits.MaximumCallStack = 256
	}
	if limits.Concurrency < 1 || limits.Concurrency > 64 || limits.Queue < 1 || limits.Queue > 1024 ||
		limits.AdmissionTimeout <= 0 || limits.AdmissionTimeout > 5*time.Second || limits.DefaultTimeout <= 0 ||
		limits.MaximumTimeout < limits.DefaultTimeout || limits.MaximumTimeout > 5*time.Minute ||
		limits.MaximumHostCalls < 1 || limits.MaximumHostCalls > 4096 || limits.MaximumInputBytes < 1 ||
		limits.MaximumInputBytes > 4<<20 || limits.MaximumOutputBytes < 1 || limits.MaximumOutputBytes > 16<<20 ||
		limits.MaximumJSONDepth < 1 || limits.MaximumJSONDepth > 128 || limits.MaximumCallStack < 32 || limits.MaximumCallStack > 4096 {
		return Limits{}, errors.New("invalid JavaScript limits")
	}
	return limits, nil
}

// NormalizedForPublic lets the embedding options module apply the exact same
// defaults and ceilings without copying runtime policy.
func (limits Limits) NormalizedForPublic() (Limits, error) { return limits.normalized() }

type Runtime struct {
	bundle      *Bundle
	limits      Limits
	registry    *require.Registry
	states      sync.Map
	slots       chan struct{}
	root        context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	closed      bool
	waiting     int
	active      sync.WaitGroup
	initializer func(*goja.Runtime) error
	bodyLimits  sync.Map
}

type callState struct {
	ctx            context.Context
	host           Host
	capabilities   map[string]bool
	hostCalls      *atomic.Int64
	limits         Limits
	requestHandle  *goja.Object
	responseHandle *goja.Object
	responseUsed   bool
	apiErrorCheck  goja.Callable
}

type executionChainKey struct{}
type executionChain struct {
	runtime   *Runtime
	depth     int
	hostCalls *atomic.Int64
}

const virtualRoot = "/pgbase-js"

func NewRuntime(bundle *Bundle, limits Limits) (*Runtime, error) {
	return NewRuntimeWithInitializer(bundle, limits, nil)
}

// NewRuntimeWithInitializer installs trusted host bindings into every fresh VM
// used for validation and execution. The callback must be deterministic and
// must not retain the VM after it returns.
func NewRuntimeWithInitializer(bundle *Bundle, limits Limits, initializer func(*goja.Runtime) error) (*Runtime, error) {
	if bundle == nil {
		return nil, errors.New("JavaScript bundle is nil")
	}
	normalized, err := limits.normalized()
	if err != nil {
		return nil, err
	}
	root, cancel := context.WithCancel(context.Background())
	runtime := &Runtime{bundle: bundle, limits: normalized, slots: make(chan struct{}, normalized.Concurrency), root: root, cancel: cancel, initializer: initializer}
	runtime.registry = require.NewRegistry(
		require.WithLoader(runtime.loadSource),
		require.WithPathResolver(resolveModulePath),
	)
	runtime.registry.RegisterNativeModule("pgbase", runtime.loadNativeModule)
	if err := runtime.collectBootstrap(); err != nil {
		cancel()
		return nil, err
	}
	validationContext, validationCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer validationCancel()
	if err := runtime.validateHandlers(validationContext); err != nil {
		cancel()
		return nil, err
	}
	return runtime, nil
}

func (r *Runtime) Digest() string {
	if r == nil || r.bundle == nil {
		return ""
	}
	return r.bundle.Digest()
}

func (r *Runtime) Invoke(ctx context.Context, invocation Invocation) (output json.RawMessage, err error) {
	if r == nil {
		return nil, &Error{Code: CodeRuntimeClosed}
	}
	if closer, ok := invocation.Host.(interface{ Close() error }); ok {
		defer func() {
			if closeErr := closer.Close(); closeErr != nil {
				err = errors.Join(err, closeErr)
			}
		}()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	chain, nested := ctx.Value(executionChainKey{}).(*executionChain)
	if nested && chain.runtime == r {
		if chain.depth >= 8 {
			return nil, &Error{Code: CodeLimitExceeded, cause: errors.New("JavaScript nested execution depth exceeded")}
		}
		chain = &executionChain{runtime: r, depth: chain.depth + 1, hostCalls: chain.hostCalls}
		ctx = context.WithValue(ctx, executionChainKey{}, chain)
	} else {
		release, err := r.acquire(ctx)
		if err != nil {
			return nil, err
		}
		defer release()
		chain = &executionChain{runtime: r, hostCalls: &atomic.Int64{}}
		ctx = context.WithValue(ctx, executionChainKey{}, chain)
	}
	if len(invocation.Input) == 0 {
		invocation.Input = json.RawMessage(`{}`)
	}
	if len(invocation.Input) > r.limits.MaximumInputBytes {
		return nil, &Error{Code: CodeLimitExceeded}
	}
	timeout := invocation.Timeout
	if timeout == 0 {
		timeout = r.limits.DefaultTimeout
	}
	if timeout <= 0 || timeout > r.limits.MaximumTimeout {
		return nil, &Error{Code: CodeLimitExceeded}
	}
	callContext, cancel := context.WithTimeout(ctx, timeout)
	stopRuntimeCancel := context.AfterFunc(r.root, cancel)
	defer func() {
		stopRuntimeCancel()
		cancel()
	}()
	state := &callState{ctx: callContext, host: invocation.Host, capabilities: capabilitySet(invocation.Capabilities), hostCalls: chain.hostCalls, limits: r.limits}
	return r.invoke(callContext, invocation.Handler, invocation.Input, state, invocation.OnSkipSuccessActivityLog)
}

func (r *Runtime) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		r.cancel()
	}
	r.mu.Unlock()
	done := make(chan struct{})
	go func() {
		r.active.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Runtime) acquire(ctx context.Context) (func(), error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, &Error{Code: CodeRuntimeClosed}
	}
	select {
	case r.slots <- struct{}{}:
		r.active.Add(1)
		r.mu.Unlock()
		return r.release, nil
	default:
	}
	if r.waiting >= r.limits.Queue {
		r.mu.Unlock()
		return nil, &Error{Code: CodeBusy}
	}
	r.waiting++
	r.mu.Unlock()
	timer := time.NewTimer(r.limits.AdmissionTimeout)
	defer timer.Stop()
	var acquireErr error
	select {
	case r.slots <- struct{}{}:
	case <-ctx.Done():
		acquireErr = invocationContextError(ctx)
	case <-r.root.Done():
		acquireErr = &Error{Code: CodeRuntimeClosed}
	case <-timer.C:
		acquireErr = &Error{Code: CodeBusy}
	}
	r.mu.Lock()
	r.waiting--
	if acquireErr == nil && r.closed {
		<-r.slots
		acquireErr = &Error{Code: CodeRuntimeClosed}
	}
	if acquireErr == nil {
		r.active.Add(1)
	}
	r.mu.Unlock()
	if acquireErr != nil {
		return nil, acquireErr
	}
	return r.release, nil
}

func (r *Runtime) release() {
	<-r.slots
	r.active.Done()
}

func (r *Runtime) invoke(ctx context.Context, ref HandlerRef, input json.RawMessage, state *callState, onSkipSuccessActivityLog func()) (json.RawMessage, error) {
	moduleName, err := r.bundle.canonicalModule(ref.Module)
	if err != nil {
		return nil, &Error{Code: CodeExecutionFailed, cause: err}
	}
	vm := goja.New()
	if r.initializer != nil {
		if err := r.initializer(vm); err != nil {
			return nil, &Error{Code: CodeExecutionFailed, cause: fmt.Errorf("initialize JavaScript VM: %w", err)}
		}
	}
	vm.SetMaxCallStackSize(r.limits.MaximumCallStack)
	vm.SetParserOptions(parser.WithDisableSourceMaps)
	r.states.Store(vm, state)
	defer r.states.Delete(vm)
	requireModule := r.registry.Enable(vm)
	if err := r.installPocketBaseGlobals(vm, state); err != nil {
		return nil, &Error{Code: CodeExecutionFailed, cause: err}
	}

	finished := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			vm.Interrupt(ctx.Err())
		case <-finished:
		}
	}()
	defer func() {
		close(finished)
		<-watcherDone
	}()

	var handler goja.Callable
	var handlerValue goja.Value
	var ok bool
	if strings.HasPrefix(ref.Export, registeredExportPrefix) {
		registered, replayErr := r.replayRegistrations(vm, requireModule)
		if replayErr != nil {
			return nil, classifyExecutionError(ctx, vm, state, replayErr, r.limits.MaximumJSONDepth)
		}
		handler, ok = registered.handlers[ref.Export]
		handlerValue = registered.handlerValues[ref.Export]
	} else {
		exports, requireErr := requireModule.Require(virtualRoot + "/" + moduleName)
		if requireErr != nil {
			return nil, classifyExecutionError(ctx, vm, state, requireErr, r.limits.MaximumJSONDepth)
		}
		handlerValue = exports.ToObject(vm).Get(ref.Export)
		handler, ok = goja.AssertFunction(handlerValue)
	}
	if !ok {
		return nil, &Error{Code: CodeExecutionFailed, cause: fmt.Errorf("export %q is not a function", ref.Export)}
	}
	if preparer, ok := invocationHostPreparer(state.host); ok {
		bodyLimit, err := javaScriptHandlerBodyLimit(vm, handlerValue)
		if err != nil {
			return nil, &Error{Code: CodeInvalidResult, cause: err}
		}
		key := ref.Module + "\x00" + ref.Export
		if bodyLimit == 0 {
			if cached, ok := r.bodyLimits.Load(key); ok {
				bodyLimit = cached.(int64)
			}
		} else {
			r.bodyLimits.Store(key, bodyLimit)
		}
		input, err = preparer.PrepareInput(ctx, input, bodyLimit)
		if err != nil {
			var tooLarge inputTooLarge
			if errors.As(err, &tooLarge) && tooLarge.InputTooLarge() {
				return nil, &Error{Code: CodeLimitExceeded, cause: err}
			}
			return nil, &Error{Code: CodeInvalidResult, cause: err}
		}
	}
	if onSkipSuccessActivityLog != nil && javaScriptHandlerSkipsSuccessActivityLog(vm, handlerValue) {
		onSkipSuccessActivityLog()
	}
	inputValue, err := decodeValue(vm, input, r.limits.MaximumJSONDepth)
	if err != nil {
		return nil, &Error{Code: CodeInvalidResult, cause: err}
	}
	if object, ok := inputValue.(*goja.Object); ok {
		state.requestHandle = vm.NewObject()
		state.responseHandle = vm.NewObject()
		if err := object.Set("request", state.requestHandle); err != nil {
			return nil, &Error{Code: CodeInvalidResult, cause: err}
		}
		if err := object.Set("response", state.responseHandle); err != nil {
			return nil, &Error{Code: CodeInvalidResult, cause: err}
		}
	}
	result, err := handler(goja.Undefined(), inputValue)
	if err != nil {
		return nil, classifyExecutionError(ctx, vm, state, err, r.limits.MaximumJSONDepth)
	}
	if object, ok := result.(*goja.Object); ok {
		if then := object.Get("then"); !goja.IsUndefined(then) && !goja.IsNull(then) {
			if _, callable := goja.AssertFunction(then); callable {
				return nil, &Error{Code: CodeInvalidResult, cause: errors.New("Promise and thenable results are not supported")}
			}
		}
	}
	encoded, err := encodeValue(result, r.limits.MaximumJSONDepth)
	if err != nil {
		return nil, &Error{Code: CodeInvalidResult, cause: err}
	}
	if len(encoded) > r.limits.MaximumOutputBytes {
		return nil, &Error{Code: CodeLimitExceeded}
	}
	return encoded, nil
}

func invocationHostPreparer(host Host) (InputPreparer, bool) {
	preparer, ok := host.(InputPreparer)
	return preparer, ok
}

func javaScriptHandlerBodyLimit(vm *goja.Runtime, handler goja.Value) (int64, error) {
	if handler == nil || goja.IsUndefined(handler) || goja.IsNull(handler) {
		return 0, nil
	}
	value := handler.ToObject(vm).Get("pgbaseBodyLimit")
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return 0, nil
	}
	limit := value.ToInteger()
	if limit < 1 || limit > 1<<31-1 || value.ToFloat() != float64(limit) {
		return 0, errors.New("invalid JavaScript body limit")
	}
	return limit, nil
}

func javaScriptHandlerSkipsSuccessActivityLog(vm *goja.Runtime, handler goja.Value) bool {
	if handler == nil || goja.IsUndefined(handler) || goja.IsNull(handler) {
		return false
	}
	value := handler.ToObject(vm).Get("pgbaseSkipSuccessActivityLog")
	return value != nil && value.ToBoolean()
}

func (r *Runtime) validateHandlers(ctx context.Context) error {
	manifest := r.bundle.Manifest()
	refs := make([]HandlerRef, 0, len(manifest.Routes)+len(manifest.Jobs)+len(manifest.Hooks)*2+len(manifest.Migrations)*2)
	for _, route := range manifest.Routes {
		refs = append(refs, route.Handler)
	}
	for _, hook := range manifest.Hooks {
		if hook.Before != nil {
			refs = append(refs, *hook.Before)
		}
		if hook.After != nil {
			refs = append(refs, *hook.After)
		}
	}
	for _, job := range manifest.Jobs {
		refs = append(refs, job.Handler)
	}
	for _, migration := range manifest.Migrations {
		refs = append(refs, HandlerRef{Module: migration.Module, Export: migration.Up})
		if migration.Down != "" {
			refs = append(refs, HandlerRef{Module: migration.Module, Export: migration.Down})
		}
	}
	for _, ref := range refs {
		state := &callState{ctx: ctx, capabilities: map[string]bool{}, hostCalls: &atomic.Int64{}, limits: r.limits}
		vm := goja.New()
		if r.initializer != nil {
			if err := r.initializer(vm); err != nil {
				return fmt.Errorf("initialize JavaScript VM: %w", err)
			}
		}
		vm.SetMaxCallStackSize(r.limits.MaximumCallStack)
		vm.SetParserOptions(parser.WithDisableSourceMaps)
		r.states.Store(vm, state)
		requireModule := r.registry.Enable(vm)
		if err := r.installPocketBaseGlobals(vm, state); err != nil {
			return fmt.Errorf("initialize PocketBase compatibility globals: %w", err)
		}
		canonical, _ := r.bundle.canonicalModule(ref.Module)
		if strings.HasPrefix(ref.Export, registeredExportPrefix) {
			registered, err := r.replayRegistrations(vm, requireModule)
			r.states.Delete(vm)
			if err != nil {
				return fmt.Errorf("compile registered JavaScript handler %s: %w", ref.Export, err)
			}
			if registered.handlers[ref.Export] == nil {
				return fmt.Errorf("registered JavaScript handler %s is missing", ref.Export)
			}
			continue
		}
		exports, err := requireModule.Require(virtualRoot + "/" + canonical)
		r.states.Delete(vm)
		if err != nil {
			return fmt.Errorf("compile JavaScript handler %s:%s: %w", ref.Module, ref.Export, err)
		}
		if _, ok := goja.AssertFunction(exports.ToObject(vm).Get(ref.Export)); !ok {
			return fmt.Errorf("JavaScript handler %s:%s is not a function", ref.Module, ref.Export)
		}
	}
	return nil
}

func (r *Runtime) loadSource(filename string) ([]byte, error) {
	if !strings.HasPrefix(filename, virtualRoot+"/") {
		return nil, require.ModuleFileDoesNotExistError
	}
	name := strings.TrimPrefix(filename, virtualRoot+"/")
	canonical, err := r.bundle.canonicalModule(name)
	if err != nil || canonical != name {
		return nil, require.ModuleFileDoesNotExistError
	}
	return r.bundle.Source(name), nil
}

func resolveModulePath(base, requested string) string {
	if strings.Contains(requested, "\\") || strings.IndexByte(requested, 0) >= 0 {
		return virtualRoot + "/__invalid__"
	}
	if strings.HasPrefix(requested, "/") {
		cleaned := path.Clean(requested)
		if cleaned == virtualRoot || strings.HasPrefix(cleaned, virtualRoot+"/") {
			return cleaned
		}
		return virtualRoot + "/__invalid__"
	}
	if !strings.HasPrefix(requested, "./") && !strings.HasPrefix(requested, "../") {
		return virtualRoot + "/__invalid__"
	}
	cleaned := path.Clean(path.Join(base, requested))
	if cleaned == virtualRoot || strings.HasPrefix(cleaned, virtualRoot+"/") {
		return cleaned
	}
	return virtualRoot + "/__invalid__"
}

func (r *Runtime) loadNativeModule(vm *goja.Runtime, module *goja.Object) {
	loaded, ok := r.states.Load(vm)
	if !ok {
		panic(vm.NewGoError(errors.New("JavaScript call state is unavailable")))
	}
	state := loaded.(*callState)
	exports := vm.NewObject()
	for namespace, methods := range map[string][]string{
		"records":   {"list", "get", "create", "update", "delete", "batch"},
		"mail":      {"send"},
		"files":     {"list", "upload", "delete", "token", "read", "open", "readChunk", "close"},
		"migration": {"query", "exec", "applySnapshot"},
		"log":       {"debug", "info", "warn", "error"},
		"template":  {"render"},
		"database":  {"transaction"},
		"objects":   {"read", "write", "delete", "list", "copy"},
	} {
		object := vm.NewObject()
		for _, method := range methods {
			fullMethod := namespace + "." + method
			if err := object.Set(method, r.hostFunction(vm, state, namespace, fullMethod)); err != nil {
				panic(vm.NewGoError(err))
			}
		}
		if err := exports.Set(namespace, object); err != nil {
			panic(vm.NewGoError(err))
		}
	}
	if err := exports.Set("exact", func(call goja.FunctionCall) goja.Value {
		value := call.Argument(0).String()
		if !exactNumberPattern.MatchString(value) {
			panic(vm.NewTypeError("exact number must be a valid JSON number"))
		}
		return exactValue(vm, value)
	}); err != nil {
		panic(vm.NewGoError(err))
	}
	if err := module.Set("exports", exports); err != nil {
		panic(vm.NewGoError(err))
	}
}

func (r *Runtime) hostFunction(vm *goja.Runtime, state *callState, capability, method string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if !state.capabilities[capability] {
			panic(vm.ToValue(map[string]any{"name": "PgBaseError", "code": string(CodeCapabilityDenied)}))
		}
		if state.host == nil {
			panic(vm.NewGoError(errors.New("host operations are unavailable during module validation")))
		}
		if state.hostCalls.Add(1) > int64(state.limits.MaximumHostCalls) {
			panic(vm.ToValue(map[string]any{"name": "PgBaseError", "code": string(CodeLimitExceeded)}))
		}
		input := goja.Value(goja.Undefined())
		if len(call.Arguments) > 0 {
			input = call.Argument(0)
		}
		encoded, err := encodeValue(input, state.limits.MaximumJSONDepth)
		if err != nil || len(encoded) > state.limits.MaximumInputBytes {
			panic(vm.ToValue(map[string]any{"name": "PgBaseError", "code": string(CodeLimitExceeded)}))
		}
		result, err := state.host.Call(state.ctx, method, encoded)
		if err != nil {
			panic(vm.ToValue(hostException(err)))
		}
		if len(result) > state.limits.MaximumOutputBytes {
			panic(vm.ToValue(map[string]any{"name": "PgBaseError", "code": string(CodeLimitExceeded)}))
		}
		value, err := decodeValue(vm, result, state.limits.MaximumJSONDepth)
		if err != nil {
			panic(vm.NewGoError(errors.New("host returned invalid JSON")))
		}
		return value
	}
}

type codedError interface {
	CodeValue() string
}

func hostException(err error) map[string]any {
	result := map[string]any{"name": "PgBaseError", "code": "host_failed"}
	var coded codedError
	if errors.As(err, &coded) && coded.CodeValue() != "" {
		result["code"] = coded.CodeValue()
	}
	return result
}

func capabilitySet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func classifyExecutionError(ctx context.Context, vm *goja.Runtime, state *callState, err error, maximumJSONDepth int) error {
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) || ctx.Err() != nil {
		return invocationContextError(ctx)
	}
	return &Error{Code: CodeExecutionFailed, PublicError: publicAPIError(vm, state, err, maximumJSONDepth), cause: err}
}

func publicAPIError(vm *goja.Runtime, state *callState, err error, maximumJSONDepth int) (result *PublicAPIError) {
	var exception *goja.Exception
	if vm == nil || state == nil || state.apiErrorCheck == nil || !errors.As(err, &exception) {
		return nil
	}
	object, ok := exception.Value().(*goja.Object)
	if !ok {
		return nil
	}
	defer func() { _ = recover() }()
	branded, checkErr := state.apiErrorCheck(goja.Undefined(), object)
	if checkErr != nil || !branded.ToBoolean() {
		return nil
	}
	status, ok := apiErrorStatus(object.Get("status"))
	if !ok {
		return nil
	}
	message, ok := object.Get("message").Export().(string)
	if !ok || len(message) > 8<<10 {
		return nil
	}
	data, encodeErr := encodeValue(object.Get("data"), maximumJSONDepth)
	if encodeErr != nil || len(data) > 1<<20 {
		return nil
	}
	return &PublicAPIError{Status: status, Message: message, Data: data}
}

func apiErrorStatus(value goja.Value) (int, bool) {
	switch status := value.Export().(type) {
	case int64:
		if status >= 100 && status <= 599 {
			return int(status), true
		}
	case float64:
		if status >= 100 && status <= 599 && status == float64(int(status)) {
			return int(status), true
		}
	}
	return 0, false
}

func invocationContextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &Error{Code: CodeTimeout, cause: context.DeadlineExceeded}
	}
	return &Error{Code: CodeCanceled, cause: context.Canceled}
}
