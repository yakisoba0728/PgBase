package jsvm_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/jsvm"
)

type recordingHost struct {
	calls []string
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
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{DefaultTimeout: 25 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	_, err = runtime.Invoke(context.Background(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler})
	var executionError *jsvm.Error
	if !errors.As(err, &executionError) || executionError.Code != jsvm.CodeTimeout {
		t.Fatalf("infinite loop error = %#v", err)
	}
	output, err := runtime.Invoke(context.Background(), jsvm.Invocation{Handler: bundle.Manifest().Routes[1].Handler})
	if err != nil || string(output) != `{"ok":true}` {
		t.Fatalf("subsequent invocation output=%s err=%v", output, err)
	}
}
