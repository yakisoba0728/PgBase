//go:build integration

package pgbase

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/backup"
	"github.com/yakisoba0728/PgBase/internal/jsvm"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestJavaScriptMigrationsUseExistingLedgerAndRollback(t *testing.T) {
	for _, format := range []string{jsvm.LegacyManifestFormat, jsvm.ManifestFormat} {
		t.Run(format, func(t *testing.T) { testJavaScriptMigrationLedger(t, format) })
	}
}

func testJavaScriptMigrationLedger(t *testing.T, format string) {
	directory := t.TempDir()
	manifest := `{
 "format":"pgbase.scripts/v1",
 "migrations":[
  {"id":"202609100001_create_widget","module":"first.js","up":"up","down":"down","dependencies":["shared.js"]},
  {"id":"202609100002_fail_widget","module":"second.js","up":"up","irreversibleReason":"fixture never applies"}
 ]
}`
	manifest = strings.Replace(manifest, jsvm.LegacyManifestFormat, format, 1)
	for name, source := range map[string]string{
		"pgbase.scripts.json": manifest,
		"shared.js":           `exports.table="n6_extension.widgets";`,
		"first.js": `const pg=require("pgbase"), shared=require("./shared");
exports.up=function(){
 pg.migration.exec({sql:"create schema n6_extension"});
 pg.migration.exec({sql:"create table "+shared.table+"(id bigint primary key,label text not null)"});
 pg.migration.exec({sql:"insert into "+shared.table+"(id,label) values($1::bigint,$2)",args:[pg.exact("9007199254740993"),"exact"]});
 const rows=pg.migration.query({sql:"select id::text as id,label from "+shared.table});
 if(rows.rows[0].id!=="9007199254740993" || rows.rows[0].label!=="exact") throw new Error("query mismatch");
 return {applied:true};
};
exports.down=function(){pg.migration.exec({sql:"drop schema n6_extension cascade"});return {reverted:true};};`,
		"second.js": `const pg=require("pgbase"); exports.up=function(){pg.migration.exec({sql:"create table n6_failed(value text)"});throw new Error("rollback fixture");};`,
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.JavaScript = &JavaScriptOptions{Directory: directory}
	})
	migrations, err := fixture.app.JavaScriptMigrations()
	if err != nil {
		t.Fatal(err)
	}
	sourceFormat := "pgbase.js-migration-source/v1"
	if format == jsvm.ManifestFormat {
		sourceFormat = "pgbase.js-migration-source/v2"
	}
	if len(migrations) != 2 || !strings.Contains(string(migrations[0].Source), sourceFormat) {
		t.Fatalf("migrations = %#v", migrations)
	}
	runner, err := NewUserMigrationRunner(fixture.app, migrations)
	if err != nil {
		t.Fatal(err)
	}
	results, err := runner.Up(context.Background())
	if err == nil || len(results) != 1 || results[0].Migration.ID != "202609100001_create_widget" {
		t.Fatalf("Up results=%#v error=%v", results, err)
	}
	var firstExists, failedExists bool
	if err := fixture.app.pool.QueryRow(context.Background(), `select to_regclass('n6_extension.widgets') is not null,to_regclass('public.n6_failed') is not null`).Scan(&firstExists, &failedExists); err != nil {
		t.Fatal(err)
	}
	if !firstExists || failedExists {
		t.Fatalf("transaction state first=%t failed=%t", firstExists, failedExists)
	}
	history, err := runner.History(context.Background())
	if err != nil || len(history) != 2 || history[0].State != UserMigrationHistoryApplied || history[1].State != UserMigrationHistoryPending {
		t.Fatalf("history=%#v error=%v", history, err)
	}
	down, err := runner.Down(context.Background())
	if err != nil || down.Migration.ID != "202609100001_create_widget" {
		t.Fatalf("Down=%#v error=%v", down, err)
	}
	if err := fixture.app.pool.QueryRow(context.Background(), `select to_regclass('n6_extension.widgets') is not null`).Scan(&firstExists); err != nil {
		t.Fatal(err)
	}
	if firstExists {
		t.Fatal("Down left extension table behind")
	}
}

func TestJavaScriptMigrationUpgradePreservesLedgerAndRestore(t *testing.T) {
	ctx := t.Context()
	directory := t.TempDir()
	first := `{"id":"202611130001_legacy","module":"first.js","up":"up","down":"down"}`
	write := func(name, source string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(directory, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("pgbase.scripts.json", `{"format":"pgbase.scripts/v1","migrations":[`+first+`]}`)
	write("first.js", `if(typeof $app==='undefined')throw new Error('legacy validation');const pg=require('pgbase');exports.up=()=>{if(typeof $app==='undefined')throw new Error('legacy execution');pg.migration.exec({sql:"create table pgbase_meta.js_runtime_probe(mode text);insert into pgbase_meta.js_runtime_probe values('legacy')"});return {}};exports.down=()=>{if(typeof $app==='undefined')throw new Error('legacy down');pg.migration.exec({sql:"drop table pgbase_meta.js_runtime_probe"});return {}};`)
	fixture := newPublicUsersAppOptions(t, false, nil)
	migrations := func() []UserMigration {
		t.Helper()
		bundle, err := jsvm.LoadBundle(directory)
		if err != nil {
			t.Fatal(err)
		}
		engine, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		defer engine.Close(ctx)
		app := &App{javaScriptRuntime: &javaScriptRuntime{bundle: bundle}}
		result, err := app.JavaScriptMigrations()
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	old := migrations()
	runner, err := NewUserMigrationRunner(fixture.app, old)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := runner.Up(ctx); err != nil || len(result) != 1 {
		t.Fatalf("legacy Up=%v, %v", result, err)
	}
	first = strings.TrimSuffix(first, "}") + `,"runtimeFormat":"pgbase.scripts/v1"}`
	write("pgbase.scripts.json", `{"format":"pgbase.scripts/v2","migrations":[`+first+`,{"id":"202611130002_native","module":"second.js","up":"up","down":"down"}]}`)
	write("second.js", `if(typeof $app!=='undefined')throw new Error('native validation');const pg=require('pgbase');exports.up=()=>{if(typeof $app!=='undefined')throw new Error('native execution');pg.migration.exec({sql:"insert into pgbase_meta.js_runtime_probe values('native')"});return {}};exports.down=()=>{if(typeof $app!=='undefined')throw new Error('native down');pg.migration.exec({sql:"delete from pgbase_meta.js_runtime_probe where mode='native'"});return {}};`)
	mixed := migrations()
	if !bytes.Equal(old[0].Source, mixed[0].Source) {
		t.Fatal("upgrade changed legacy checksum authority")
	}
	runner, err = NewUserMigrationRunner(fixture.app, mixed)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := runner.Up(ctx); err != nil || len(result) != 1 || result[0].Migration.ID != mixed[1].ID {
		t.Fatalf("mixed Up=%v, %v", result, err)
	}
	assertDown := func(t *testing.T, app *App) {
		t.Helper()
		r, err := NewUserMigrationRunner(app, mixed)
		if err != nil {
			t.Fatal(err)
		}
		history, err := r.History(ctx)
		if err != nil || len(history) != 2 || history[0].State != UserMigrationHistoryApplied || history[1].State != UserMigrationHistoryApplied {
			t.Fatalf("history=%v, %v", history, err)
		}
		for _, id := range []string{mixed[1].ID, mixed[0].ID} {
			result, err := r.Down(ctx)
			if err != nil || result.Migration.ID != id {
				t.Fatalf("Down=%v, %v", result, err)
			}
		}
		var exists bool
		if err := app.pool.QueryRow(ctx, `select to_regclass('pgbase_meta.js_runtime_probe') is not null`).Scan(&exists); err != nil || exists {
			t.Fatalf("Down left table=%v, %v", exists, err)
		}
	}
	t.Run("restore", func(t *testing.T) {
		if _, err := exec.LookPath("pg_dump"); err != nil {
			t.Skip("pg_dump unavailable")
		}
		snapshot := filepath.Join(t.TempDir(), "snapshot")
		if err := backup.CreateOnline(ctx, snapshot, fixture.app.options.DatabaseURL, nil); err != nil {
			t.Fatal(err)
		}
		target := testutil.NewDatabase(t)
		if err := backup.Restore(ctx, snapshot, target, filepath.Join(t.TempDir(), "files")); err != nil {
			t.Fatal(err)
		}
		options := fixture.app.options
		options.DatabaseURL = target
		restored, err := New(ctx, options)
		if err != nil {
			t.Fatal(err)
		}
		defer restored.Close(ctx)
		var rows int
		if err := restored.pool.QueryRow(ctx, `select count(*) from pgbase_meta.js_runtime_probe`).Scan(&rows); err != nil || rows != 2 {
			t.Fatalf("restored rows=%d, %v", rows, err)
		}
		assertDown(t, restored)
	})
	assertDown(t, fixture.app)
}
