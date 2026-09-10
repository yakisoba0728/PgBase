//go:build integration

package pgbase

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestJavaScriptTrustedDatabaseRollbackAndLogicalObjectIsolation(t *testing.T) {
	directory, files := t.TempDir(), t.TempDir()
	manifest := `{"format":"pgbase.scripts/v1","routes":[
 {"method":"POST","pattern":"/extensions/database","access":"public","handler":{"module":"database.js","export":"run"},"capabilities":["database"]},
 {"method":"POST","pattern":"/extensions/object-a","access":"public","handler":{"module":"a.js","export":"run"},"capabilities":["objects"]},
 {"method":"POST","pattern":"/extensions/object-b","access":"public","handler":{"module":"b.js","export":"run"},"capabilities":["objects"]}
]}`
	sources := map[string]string{
		"pgbase.scripts.json": manifest,
		"database.js":         `const pg=require("pgbase");exports.run=function(){let rolled=false;try{pg.database.transaction({statements:[{sql:"insert into app.extension_sql(value) values($1)",args:["rolled back"]},{sql:"select * from app.missing_extension_table",query:true}]})}catch(e){rolled=true}const out=pg.database.transaction({statements:[{sql:"select count(*)::text as count from app.extension_sql",query:true}]});return {status:200,json:{rolled,count:out.results[0].items[0].count}}}`,
		"a.js":                `const pg=require("pgbase");exports.run=function(){pg.objects.write({key:"shared",data:"cGVyc2lzdGVk"});return {status:200,json:pg.objects.read({key:"shared"})}}`,
		"b.js":                `const pg=require("pgbase");exports.run=function(){try{return {status:200,json:pg.objects.read({key:"shared"})}}catch(e){return {status:404,json:{code:e.code}}}}`,
	}
	for name, source := range sources {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var objectWrites, objectDeletes atomic.Int32
	hooks := &HookRegistry{}
	if _, err := hooks.On("object.write", Hook{Before: func(context.Context, *HookEvent) error { objectWrites.Add(1); return nil }}); err != nil {
		t.Fatal(err)
	}
	if _, err := hooks.On("object.delete", Hook{Before: func(context.Context, *HookEvent) error { objectDeletes.Add(1); return nil }}); err != nil {
		t.Fatal(err)
	}
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.EnableTrustedDatabaseAccess = true
		options.Files = FileOptions{Driver: "local", LocalDirectory: files, MaximumBytes: 1024}
		options.Hooks = hooks
		options.JavaScript = &JavaScriptOptions{Directory: directory, Capabilities: map[string]bool{"database": true, "objects": true}}
	})
	if _, err := fixture.app.pool.Exec(t.Context(), `create table app.extension_sql(value text not null)`); err != nil {
		t.Fatal(err)
	}
	call := func(path string) map[string]any {
		response := publicUsersRequest(t, fixture, http.MethodPost, path, map[string]any{}, "", nil, nil)
		var body map[string]any
		decodePublicUsersJSON(t, response, &body)
		body["status"] = response.Code
		return body
	}
	database := call("/extensions/database")
	if database["status"] != 200 || database["rolled"] != true || database["count"] != "0" {
		t.Fatalf("database=%#v", database)
	}
	object := call("/extensions/object-a")
	if object["status"] != 200 || object["data"] != "cGVyc2lzdGVk" || objectWrites.Load() != 1 {
		t.Fatalf("object=%#v writes=%d", object, objectWrites.Load())
	}
	isolation := call("/extensions/object-b")
	if isolation["status"] != 404 {
		t.Fatalf("isolation=%#v", isolation)
	}
	entries, err := filepath.Glob(filepath.Join(files, "extensions", "*", "*"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("persisted objects=%v err=%v", entries, err)
	}
	if err := fixture.app.extensions.objects.Delete(t.Context(), "a.js", "shared"); err != nil {
		t.Fatal(err)
	}
	if objectDeletes.Load() != 1 {
		t.Fatalf("object delete hooks=%d", objectDeletes.Load())
	}
	if _, err := fixture.app.extensions.objects.Read(t.Context(), "a.js", "shared"); err == nil {
		t.Fatal("deleted logical object remained readable")
	}
}

func TestJavaScriptDatabaseRequiresBothStartupAndManifestPermission(t *testing.T) {
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/db","access":"public","handler":{"module":"route.js","export":"run"},"capabilities":["database"]}]}`, `exports.run=()=>({status:204})`)
	base := Options{DatabaseURL: "postgres://postgres:postgres@localhost/pgbase", TokenSecret: make([]byte, 32), JavaScript: &JavaScriptOptions{Directory: directory, Capabilities: map[string]bool{"database": true}}}
	if _, err := base.Normalized(); err == nil {
		t.Fatal("database capability accepted without trusted database opt-in")
	}
	base.EnableTrustedDatabaseAccess = true
	base.JavaScript.Capabilities = map[string]bool{}
	normalized, err := base.Normalized()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = prepareJavaScript(*normalized.JavaScript); err == nil {
		t.Fatal("manifest database permission accepted outside application allowlist")
	}
}
