//go:build integration

package pgbase

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJavaScriptMigrationsUseExistingLedgerAndRollback(t *testing.T) {
	directory := t.TempDir()
	manifest := `{
 "format":"pgbase.scripts/v1",
 "migrations":[
  {"id":"202609100001_create_widget","module":"first.js","up":"up","down":"down","dependencies":["shared.js"]},
  {"id":"202609100002_fail_widget","module":"second.js","up":"up","irreversibleReason":"fixture never applies"}
 ]
}`
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
	if len(migrations) != 2 || !strings.Contains(string(migrations[0].Source), "pgbase.js-migration-source/v1") {
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
