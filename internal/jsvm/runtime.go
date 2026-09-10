package jsvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path"
	"regexp"
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
	Code  ErrorCode
	cause error
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

type Invocation struct {
	Handler      HandlerRef
	Capabilities []string
	Input        json.RawMessage
	Host         Host
	Timeout      time.Duration
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
}

type callState struct {
	ctx          context.Context
	host         Host
	capabilities map[string]bool
	hostCalls    *atomic.Int64
	limits       Limits
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
	return r.invoke(callContext, invocation.Handler, invocation.Input, state)
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

func (r *Runtime) invoke(ctx context.Context, ref HandlerRef, input json.RawMessage, state *callState) (json.RawMessage, error) {
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

	exports, err := requireModule.Require(virtualRoot + "/" + moduleName)
	if err != nil {
		return nil, classifyExecutionError(ctx, err)
	}
	object := exports.ToObject(vm)
	handler, ok := goja.AssertFunction(object.Get(ref.Export))
	if !ok {
		return nil, &Error{Code: CodeExecutionFailed, cause: fmt.Errorf("export %q is not a function", ref.Export)}
	}
	inputValue, err := decodeValue(vm, input, r.limits.MaximumJSONDepth)
	if err != nil {
		return nil, &Error{Code: CodeInvalidResult, cause: err}
	}
	result, err := handler(goja.Undefined(), inputValue)
	if err != nil {
		return nil, classifyExecutionError(ctx, err)
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
		canonical, _ := r.bundle.canonicalModule(ref.Module)
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
		"objects":   {"read", "write", "delete"},
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

func classifyExecutionError(ctx context.Context, err error) error {
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) || ctx.Err() != nil {
		return invocationContextError(ctx)
	}
	return &Error{Code: CodeExecutionFailed, cause: err}
}

func invocationContextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &Error{Code: CodeTimeout, cause: context.DeadlineExceeded}
	}
	return &Error{Code: CodeCanceled, cause: context.Canceled}
}

var exactNumberPattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)

func exactValue(vm *goja.Runtime, value string) *goja.Object {
	object := vm.NewObject()
	_ = object.DefineDataProperty("__pgbaseExact", vm.ToValue(value), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	_ = object.Set("toString", func(goja.FunctionCall) goja.Value { return vm.ToValue(value) })
	return object
}

func decodeValue(vm *goja.Runtime, raw json.RawMessage, maximumDepth int) (goja.Value, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return nil, err
	}
	return importValue(vm, decoded, 0, maximumDepth)
}

func importValue(vm *goja.Runtime, value any, depth, maximumDepth int) (goja.Value, error) {
	if depth > maximumDepth {
		return nil, errors.New("JSON nesting exceeds limit")
	}
	switch value := value.(type) {
	case nil:
		return goja.Null(), nil
	case bool, string:
		return vm.ToValue(value), nil
	case json.Number:
		return exactValue(vm, value.String()), nil
	case []any:
		array := vm.NewArray(len(value))
		for index, item := range value {
			converted, err := importValue(vm, item, depth+1, maximumDepth)
			if err != nil {
				return nil, err
			}
			if err := array.Set(fmt.Sprint(index), converted); err != nil {
				return nil, err
			}
		}
		return array, nil
	case map[string]any:
		object := vm.NewObject()
		for name, item := range value {
			converted, err := importValue(vm, item, depth+1, maximumDepth)
			if err != nil {
				return nil, err
			}
			if err := object.Set(name, converted); err != nil {
				return nil, err
			}
		}
		return object, nil
	default:
		return nil, errors.New("unsupported decoded JSON value")
	}
}

func encodeValue(value goja.Value, maximumDepth int) (json.RawMessage, error) {
	converted, err := exportValue(value, 0, maximumDepth, make(map[*goja.Object]bool))
	if err != nil {
		return nil, err
	}
	return json.Marshal(converted)
}

func exportValue(value goja.Value, depth, maximumDepth int, seen map[*goja.Object]bool) (any, error) {
	if depth > maximumDepth {
		return nil, errors.New("JSON nesting exceeds limit")
	}
	if goja.IsNull(value) {
		return nil, nil
	}
	if goja.IsUndefined(value) {
		if depth == 0 {
			return nil, nil
		}
		return nil, errors.New("undefined is not a JSON value")
	}
	switch exported := value.Export().(type) {
	case bool, string:
		return exported, nil
	case int64:
		if exported > 1<<53-1 || exported < -(1<<53-1) {
			return nil, errors.New("unsafe JavaScript integer must use pgbase.exact")
		}
		return exported, nil
	case float64:
		if math.IsNaN(exported) || math.IsInf(exported, 0) {
			return nil, errors.New("non-finite number is not JSON")
		}
		return exported, nil
	}
	object, ok := value.(*goja.Object)
	if !ok {
		return nil, errors.New("unsupported JavaScript value")
	}
	if marker := object.Get("__pgbaseExact"); marker != nil && !goja.IsUndefined(marker) {
		exact, ok := marker.Export().(string)
		if !ok || !exactNumberPattern.MatchString(exact) {
			return nil, errors.New("invalid exact number")
		}
		return json.Number(exact), nil
	}
	if seen[object] {
		return nil, errors.New("cyclic object is not JSON")
	}
	seen[object] = true
	defer delete(seen, object)
	if object.ClassName() == "Array" {
		length := object.Get("length").ToInteger()
		if length < 0 || length > 1_000_000 {
			return nil, errors.New("array length exceeds limit")
		}
		result := make([]any, int(length))
		for index := range result {
			converted, err := exportValue(object.Get(fmt.Sprint(index)), depth+1, maximumDepth, seen)
			if err != nil {
				return nil, err
			}
			result[index] = converted
		}
		return result, nil
	}
	if object.ClassName() != "Object" {
		return nil, fmt.Errorf("unsupported JavaScript object %s", object.ClassName())
	}
	result := make(map[string]any)
	for _, name := range object.Keys() {
		converted, err := exportValue(object.Get(name), depth+1, maximumDepth, seen)
		if err != nil {
			return nil, err
		}
		result[name] = converted
	}
	return result, nil
}
