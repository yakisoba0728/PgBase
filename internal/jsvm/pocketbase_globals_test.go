package jsvm_test

import (
	"context"
	"encoding/json"
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
