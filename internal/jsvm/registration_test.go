package jsvm_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/jsvm"
)

func TestBootstrapRegistrationsReplayClosuresForRouteHookAndJob(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","bootstrap":"bootstrap.js","routes":[{"method":"GET","pattern":"/registered","access":"public","handler":{"module":"placeholder.js","export":"run"}}],"hooks":[{"event":"record.create","priority":3,"before":{"module":"placeholder.js","export":"run"}}],"jobs":[{"name":"cleanup","schedule":"0 * * * *","handler":{"module":"placeholder.js","export":"run"}}]}`)
	writeFile(t, directory, "placeholder.js", `exports.run=()=>({placeholder:true})`)
	writeFile(t, directory, "bootstrap.js", `const prefix="captured";routerUse((next)=>(e)=>{const r=next(e);r.middleware=true;return r});routerAdd("GET","/registered",(e)=>({kind:"route",prefix,value:e.value}));onRecordCreateRequest((e)=>({kind:"hook",prefix,value:e.value}));cronAdd("cleanup","0 * * * *",(e)=>({kind:"job",prefix,value:e.value}))`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	manifest := bundle.Manifest()
	refs := []jsvm.HandlerRef{manifest.Routes[0].Handler, *manifest.Hooks[0].Before, manifest.Jobs[0].Handler}
	wants := []string{"route", "hook", "job"}
	for i, ref := range refs {
		if !strings.HasPrefix(ref.Export, "$registered:") {
			t.Fatalf("ref=%+v", ref)
		}
		out, err := runtime.Invoke(context.Background(), jsvm.Invocation{Handler: ref, Input: json.RawMessage(`{"value":7}`)})
		if err != nil {
			t.Fatalf("invoke %s: %v", wants[i], err)
		}
		if !strings.Contains(string(out), `"kind":"`+wants[i]+`"`) || !strings.Contains(string(out), `"prefix":"captured"`) {
			t.Fatalf("output=%s", out)
		}
	}
}

func TestBootstrapRegistrationCannotExceedManifest(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","bootstrap":"bootstrap.js"}`)
	writeFile(t, directory, "bootstrap.js", `routerAdd("GET","/undeclared",()=>({status:204}))`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = jsvm.NewRuntime(bundle, jsvm.Limits{}); err == nil || !strings.Contains(err.Error(), "not predeclared") {
		t.Fatalf("error=%v", err)
	}
}

func TestBootstrapRegistryIsFrozenBeforeInvocation(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","bootstrap":"bootstrap.js","routes":[{"method":"GET","pattern":"/frozen","access":"public","handler":{"module":"placeholder.js","export":"run"}}]}`)
	writeFile(t, directory, "placeholder.js", `exports.run=()=>({})`)
	writeFile(t, directory, "bootstrap.js", `routerAdd("GET","/frozen",()=>{routerAdd("GET","/frozen",()=>({}));return {}})`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	_, err = runtime.Invoke(context.Background(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler})
	if err == nil {
		t.Fatal("invocation-time registration succeeded")
	}
}

func TestPocketBaseConstructorsKeepMutablePrivateStateAndCanonicalExport(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/dto","access":"public","handler":{"module":"route.js","export":"run"}}]}`)
	writeFile(t, directory, "route.js", `exports.run=()=>{const r=new Record("items",{id:"one",name:"before"});r.set("name","after").unset("id");const c=new Collection({name:"items",fields:[new TextField({name:"title"})]});return {record:r.export(),collection:c.export(),message:new MailerMessage({subject:"hello"}).export(),request:new RequestInfo({method:"GET"}).export(),expression:$dbx.in("id",[1,2])}}`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	out, err := runtime.Invoke(context.Background(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"name":"after"`, `"type":"text"`, `"subject":"hello"`, `"op":"in"`} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("output=%s", out)
		}
	}
}
