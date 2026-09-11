package jsvm_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dop251/goja"
	"github.com/yakisoba0728/PgBase/internal/jsvm"
)

func TestRuntimeTranspilesTypeScriptAndInitializesEveryVM(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/typed","access":"public","handler":{"module":"typed.ts","export":"run"}}]}`)
	writeFile(t, directory, "typed.ts", `type Input={name:string}; export function run(input:Input){return {value:customPrefix+input.name}}`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	var initialized atomic.Int32
	runtime, err := jsvm.NewRuntimeWithInitializer(bundle, jsvm.Limits{}, func(vm *goja.Runtime) error { initialized.Add(1); return vm.Set("customPrefix", "hello ") })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	output, err := runtime.Invoke(context.Background(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Input: json.RawMessage(`{"name":"Ada"}`)})
	if err != nil || string(output) != `{"value":"hello Ada"}` {
		t.Fatalf("output=%s err=%v", output, err)
	}
	if initialized.Load() < 2 {
		t.Fatalf("initializer calls=%d", initialized.Load())
	}
}

type recordingHost struct {
	calls []string
}

type nestedHost struct {
	runtime *jsvm.Runtime
	handler jsvm.HandlerRef
}

type closingHost struct {
	closed atomic.Int32
}

type blockingHost struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (h *blockingHost) Call(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
	h.once.Do(func() { close(h.entered) })
	select {
	case <-h.release:
		return json.RawMessage(`{"logged":true}`), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (h *closingHost) Call(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"ok":true}`), nil
}

func (h *closingHost) Close() error {
	h.closed.Add(1)
	return nil
}

func (h nestedHost) Call(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
	return h.runtime.Invoke(ctx, jsvm.Invocation{Handler: h.handler})
}

func (h *recordingHost) Call(_ context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
	h.calls = append(h.calls, method+":"+string(input))
	return json.RawMessage(`{"id":"018f3b2e-4200-7000-8000-000000000001","amount":9007199254740993}`), nil
}

func TestRuntimeExecutesFreshCommonJSWithExactHostValues(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{
  "format":"pgbase.scripts/v1",
  "routes":[{"method":"POST","pattern":"/tasks","access":"user","handler":{"module":"routes/tasks.js","export":"create"},"capabilities":["records"]}]
}`)
	writeFile(t, directory, "routes/tasks.js", `
const pgbase = require("pgbase");
const helper = require("../lib/helper");
let calls = 0;
exports.create = function(input) {
  calls++;
  const record = pgbase.records.create({collection:"tasks", input:{amount:pgbase.exact(input.amount)}});
  return helper.response(record, calls);
};`)
	writeFile(t, directory, "lib/helper.js", `exports.response = (record, calls) => ({record, calls});`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	host := &recordingHost{}
	invocation := jsvm.Invocation{
		Handler:      bundle.Manifest().Routes[0].Handler,
		Capabilities: []string{"records"},
		Input:        json.RawMessage(`{"amount":"9007199254740993"}`),
		Host:         host,
	}
	for range 2 {
		output, err := runtime.Invoke(context.Background(), invocation)
		if err != nil {
			t.Fatalf("invoke: %v", err)
		}
		if string(output) != `{"calls":1,"record":{"amount":9007199254740993,"id":"018f3b2e-4200-7000-8000-000000000001"}}` {
			t.Fatalf("output = %s", output)
		}
	}
	if len(host.calls) != 2 || host.calls[0] != `records.create:{"collection":"tasks","input":{"amount":9007199254740993}}` {
		t.Fatalf("host calls = %#v", host.calls)
	}
}

func TestRuntimeCancelsInfiniteLoopAndRemainsAvailable(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","routes":[
 {"method":"GET","pattern":"/loop","access":"public","handler":{"module":"route.js","export":"loop"}},
 {"method":"GET","pattern":"/ok","access":"public","handler":{"module":"route.js","export":"ok"}}
]}`)
	writeFile(t, directory, "route.js", `exports.loop=()=>{for(;;){}}; exports.ok=()=>({ok:true});`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{DefaultTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	_, err = runtime.Invoke(context.Background(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Timeout: 25 * time.Millisecond})
	var executionError *jsvm.Error
	if !errors.As(err, &executionError) || executionError.Code != jsvm.CodeTimeout {
		t.Fatalf("infinite loop error = %#v", err)
	}
	output, err := runtime.Invoke(context.Background(), jsvm.Invocation{Handler: bundle.Manifest().Routes[1].Handler})
	if err != nil || string(output) != `{"ok":true}` {
		t.Fatalf("subsequent invocation output=%s err=%v", output, err)
	}
}

func TestRuntimeNestedInvocationInheritsRootSlot(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/nested","access":"public","handler":{"module":"route.js","export":"parent"},"capabilities":["log"]}]}`)
	writeFile(t, directory, "route.js", `const pg=require("pgbase"); exports.parent=()=>({nested:pg.log.info({message:"nested"})}); exports.child=()=>({ok:true});`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{Concurrency: 1, Queue: 1, AdmissionTimeout: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	output, err := runtime.Invoke(context.Background(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler,
		Capabilities: []string{"log"}, Host: nestedHost{runtime: runtime, handler: jsvm.HandlerRef{Module: "route.js", Export: "child"}}})
	if err != nil || string(output) != `{"nested":{"ok":true}}` {
		t.Fatalf("nested output=%s error=%v", output, err)
	}
}

func TestRuntimeClosesInvocationHostAfterSuccessAndInterrupt(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","routes":[
 {"method":"GET","pattern":"/call","access":"public","handler":{"module":"route.js","export":"call"},"capabilities":["log"]},
 {"method":"GET","pattern":"/loop","access":"public","handler":{"module":"route.js","export":"loop"}}
]}`)
	writeFile(t, directory, "route.js", `const pg=require("pgbase");exports.call=()=>pg.log.info({message:"close"});exports.loop=()=>{for(;;){}};`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{DefaultTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	first := &closingHost{}
	if _, err := runtime.Invoke(context.Background(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Capabilities: []string{"log"}, Host: first}); err != nil || first.closed.Load() != 1 {
		t.Fatalf("successful host closed=%d error=%v", first.closed.Load(), err)
	}
	second := &closingHost{}
	if _, err := runtime.Invoke(context.Background(), jsvm.Invocation{Handler: bundle.Manifest().Routes[1].Handler, Host: second, Timeout: 25 * time.Millisecond}); err == nil || second.closed.Load() != 1 {
		t.Fatalf("interrupted host closed=%d error=%v", second.closed.Load(), err)
	}
}

func TestRuntimeBoundsQueueAndCloseCancelsActiveAndWaitingCalls(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","routes":[
 {"method":"GET","pattern":"/block","access":"public","handler":{"module":"route.js","export":"block"},"capabilities":["log"]},
 {"method":"GET","pattern":"/loop","access":"public","handler":{"module":"route.js","export":"loop"}}
]}`)
	writeFile(t, directory, "route.js", `const pg=require("pgbase");exports.block=()=>pg.log.info({message:"block"});exports.loop=()=>{for(;;){}};`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{Concurrency: 1, Queue: 1, AdmissionTimeout: 100 * time.Millisecond, DefaultTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	host := &blockingHost{entered: make(chan struct{}), release: make(chan struct{})}
	invoke := func() error {
		_, err := runtime.Invoke(context.Background(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Capabilities: []string{"log"}, Host: host})
		return err
	}
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- invoke() }()
	<-host.entered
	go func() { second <- invoke() }()
	time.Sleep(20 * time.Millisecond)
	if _, err := runtime.Invoke(context.Background(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Capabilities: []string{"log"}, Host: host}); err == nil {
		t.Fatal("runtime accepted a call beyond the bounded queue")
	} else {
		var executionError *jsvm.Error
		if !errors.As(err, &executionError) || executionError.Code != jsvm.CodeBusy {
			t.Fatalf("queue overflow error = %v", err)
		}
	}
	close(host.release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}

	loopErrors := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := runtime.Invoke(context.Background(), jsvm.Invocation{Handler: bundle.Manifest().Routes[1].Handler})
			loopErrors <- err
		}()
	}
	time.Sleep(20 * time.Millisecond)
	closeContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Close(closeContext); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := <-loopErrors; err == nil {
			t.Fatal("runtime Close did not cancel an active or waiting call")
		}
	}
	if _, err := runtime.Invoke(context.Background(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler}); err == nil {
		t.Fatal("closed runtime accepted a new invocation")
	}
}
