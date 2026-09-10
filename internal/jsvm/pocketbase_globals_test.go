package jsvm_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/jsvm"
)

type compatibilityHost struct{ calls []string }

func (h *compatibilityHost) Call(_ context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
	h.calls = append(h.calls, method+":"+string(input))
	switch method {
	case "http.send":
		return json.RawMessage(`{"statusCode":200,"body":"ok","headers":{}}`), nil
	case "os.getenv":
		return json.RawMessage(`"configured"`), nil
	case "filesystem.list":
		return json.RawMessage(`{"objects":[{"key":"a.txt","size":2}],"nextCursor":""}`), nil
	case "records.list":
		return json.RawMessage(`{"items":[{"id":"one"}]}`), nil
	default:
		return json.RawMessage(`{"ok":true}`), nil
	}
}

func TestPocketBaseGlobalsExecuteHelpersAndCapabilityAdapters(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/compat","access":"public","handler":{"module":"route.js","export":"run"},"capabilities":["http","os","filesystem","records"]}]}`)
	writeFile(t, directory, "route.js", `exports.run=()=>({hash:$security.sha256("abc"),path:$filepath.join("a","..","b.txt"),expr:$dbx.and($dbx.in("id",[1,2]),$dbx.not(null)),http:$http.send({url:"https://allowed.test"}).body,env:$os.getenv("VISIBLE"),files:$filesystem.list(),record:$app.findFirstRecordByFilter("items","active=true")})`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	host := &compatibilityHost{}
	output, err := runtime.Invoke(context.Background(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Capabilities: []string{"http", "os", "filesystem", "records"}, Host: host})
	if err != nil {
		t.Fatal(err)
	}
	text := string(output)
	for _, want := range []string{`"http":"ok"`, `"env":"configured"`, `"id":"one"`, `"op":"and"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("output %s missing %s", text, want)
		}
	}
	if len(host.calls) != 4 {
		t.Fatalf("calls=%v", host.calls)
	}
}

func TestPocketBaseGlobalEffectsFailClosedWithoutCapability(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/denied","access":"public","handler":{"module":"route.js","export":"run"}}]}`)
	writeFile(t, directory, "route.js", `exports.run=()=> $http.send({url:"https://example.test"})`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	_, err = runtime.Invoke(context.Background(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Host: &compatibilityHost{}})
	if err == nil {
		t.Fatalf("error=%v", err)
	}
}

func TestPocketBaseFilepathGlobRequiresFilesystemCapability(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/glob","access":"public","handler":{"module":"route.js","export":"run"}}]}`)
	writeFile(t, directory, "route.js", `exports.run=()=> $filepath.glob("*.txt")`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	host := &compatibilityHost{}
	if _, err = runtime.Invoke(context.Background(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Host: host}); err == nil {
		t.Fatal("glob succeeded without filesystem capability")
	}
	if len(host.calls) != 0 {
		t.Fatalf("host calls=%v", host.calls)
	}
}

func TestPocketBaseAppControlUsesOnlyCapabilityHost(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/control","access":"admin","handler":{"module":"route.js","export":"run"},"capabilities":["app-control"]}]}`)
	writeFile(t, directory, "route.js", `exports.run=()=>{const store=$app.store();store.set("shared",{count:1});return {bootstrap:$app.bootstrap(),reset:$app.resetBootstrapState(),bootstrapped:$app.isBootstrapped(),value:store.get("shared"),deleted:store.delete("shared"),tx:$app.txInfo(),mail:typeof $app.newMailClient().send,filesystem:typeof $app.newFilesystem().local}}`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	host := &compatibilityHost{}
	if _, err := runtime.Invoke(context.Background(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Capabilities: []string{"app-control"}, Host: host}); err != nil {
		t.Fatal(err)
	}
	if len(host.calls) != 7 || !strings.HasPrefix(host.calls[0], "app-control.storeSet:") || !slices.ContainsFunc(host.calls, func(v string) bool { return strings.HasPrefix(v, "app-control.bootstrap:") }) || !slices.ContainsFunc(host.calls, func(v string) bool { return strings.HasPrefix(v, "app-control.resetBootstrapState:") }) {
		t.Fatalf("app control calls=%v", host.calls)
	}
}

func TestPocketBaseAppQueryHelpersHydrateRecords(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/query","access":"public","handler":{"module":"route.js","export":"run"},"capabilities":["records"]}]}`)
	writeFile(t, directory, "route.js", `exports.run=()=>{const rows=$app.findRecordsByIds("items",["one"]);return {id:rows[0].id,title:rows[0].getString("title"),count:$app.countRecords("items","")}}`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	host := &appQueryCompatibilityHost{}
	output, err := runtime.Invoke(context.Background(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Capabilities: []string{"records"}, Host: host})
	if err != nil || string(output) != `{"count":1,"id":"one","title":"visible"}` {
		t.Fatalf("output=%s err=%v", output, err)
	}
}

type appQueryCompatibilityHost struct{}

func (*appQueryCompatibilityHost) Call(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
	switch method {
	case "app.record.findByIds":
		return json.RawMessage(`{"items":[{"id":"one","title":"visible"}]}`), nil
	case "app.record.count":
		return json.RawMessage(`1`), nil
	}
	return nil, errors.New("unexpected method")
}

func TestPocketBaseModelFamilyDispatchesTypedHosts(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/models","access":"admin","handler":{"module":"route.js","export":"run"},"capabilities":["records"]}]}`)
	writeFile(t, directory, "route.js", `exports.run=()=>{const r=new Record("items",{title:"ok"});const q=$app.modelQuery(r).where("title='ok'").orderBy("-created").limit(2).all();return {model:new Model({table:"items"}).table,valid:$app.validate(r).valid,items:q.items.length}}`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	host := &modelCompatibilityHost{}
	output, err := runtime.Invoke(context.Background(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Capabilities: []string{"records"}, Host: host})
	if err != nil || string(output) != `{"items":1,"model":"items","valid":true}` {
		t.Fatalf("output=%s err=%v calls=%v", output, err, host.calls)
	}
}

type modelCompatibilityHost struct{ calls []string }

func (h *modelCompatibilityHost) Call(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
	h.calls = append(h.calls, method)
	switch method {
	case "app.model.query":
		return json.RawMessage(`{"items":[{"id":"one"}]}`), nil
	case "app.model.validate":
		return json.RawMessage(`{"valid":true}`), nil
	default:
		return nil, errors.New("unexpected method")
	}
}
