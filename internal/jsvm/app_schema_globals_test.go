package jsvm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/jsvm"
)

func TestPocketBaseSchemaAndDatabaseGlobalsRequireAppControlCapability(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/schema","access":"admin","handler":{"module":"route.js","export":"run"}}]}`)
	writeFile(t, directory, "route.js", `exports.run=()=> $app.db().tables()`)
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
		t.Fatal("schema introspection succeeded without app-control")
	}
	if len(host.calls) != 0 {
		t.Fatalf("host calls=%v", host.calls)
	}
}

func TestPocketBaseConstructorsAndDBXPublicMethods(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/compat","access":"public","handler":{"module":"route.js","export":"run"}}]}`)
	writeFile(t, directory, "route.js", `exports.run=()=>{
  const names=["FieldsList","Field","EditorField","PasswordField","GeoPointField","Command","Middleware","Timezone","FormData"];
  for(const name of names) if(typeof globalThis[name]!=="function") throw new Error("missing "+name);
  const fields=new FieldsList([{name:"title",type:"text"}]); if(!fields.getByName("title")) throw new Error("FieldsList");
  if(new Field({name:"ok",type:"bool"}).export().type!=="bool") throw new Error("Field");
  if(new Timezone("EET").string()!=="EET" || new Timezone("invalid").string()!=="UTC") throw new Error("Timezone");
  const form=new FormData(); form.append("a","b"); if(form.get("a")!=="b") throw new Error("FormData");
  const inExpr=$dbx.in("id",1,2,3); if(inExpr.args.length!==4 || !inExpr.build().includes("{:p2}")) throw new Error("in");
  const like=$dbx.like("title","a","b").match(true,false); if(like.args.length!==3 || !like.build().includes("AND")) throw new Error("like");
  return {status:200,json:{ok:true}};
}`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	if _, err := runtime.Invoke(context.Background(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Host: &compatibilityHost{}}); err != nil {
		t.Fatalf("%v: %v", err, errors.Unwrap(err))
	}
}
