//go:build integration

package pgbase

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/jsvm"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestJavaScriptSnapshotMigrationsPreserveRenameRelationsViewsIndexesOptionsAndHistorySync(t *testing.T) {
	source := newPublicUsersApp(t, false)
	setupPublicUsersAdministrator(t, source)
	var actor uuid.UUID
	if err := source.app.pool.QueryRow(context.Background(), `select id from pgbase_meta.admins where email=$1`, publicAdminEmail).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	service := source.app.schemaOperations.(*schema.Service)
	before, err := source.app.UserMigrationSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	minimum, maximum, relationMinimum, relationMaximum := 1, 120, 0, 1
	authors := applyN6SchemaChange(t, service, actor, schema.NewCreateChange(schema.CreateCollectionInput{Name: "authors", Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText, Required: true,
		Options: &schema.FieldOptions{MinLength: &minimum, MaxLength: &maximum, Pattern: "^[[:alpha:] ]+$"}}}}))
	posts := applyN6SchemaChange(t, service, actor, schema.NewCreateChange(schema.CreateCollectionInput{Name: "posts", Fields: []schema.FieldInput{
		{Name: "title", Type: schema.TypeText, Options: &schema.FieldOptions{MinLength: &minimum, MaxLength: &maximum}, Presentable: true, Help: "Public headline"},
		{Name: "slug", Type: schema.TypeText, Required: true},
		{Name: "author", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: authors.Name, Cardinality: schema.RelationSingle, MinSelect: &relationMinimum, MaxSelect: &relationMaximum, OnDelete: schema.RelationSetNull}},
	}, Indexes: []schema.IndexInput{{Name: "title_slug", Fields: []string{"title", "slug"}}}}))
	applyN6SchemaChange(t, service, actor, schema.NewCreateChange(schema.CreateCollectionInput{Name: "post_titles",
		View: &schema.ViewInput{Source: posts.Name, Projection: []string{"title", "slug"}}}))
	first, err := source.app.UserMigrationSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := service.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	currentPosts := catalog.Collections["posts"]
	field := func(name string) schema.Field {
		for _, candidate := range currentPosts.Fields {
			if candidate.Name == name {
				return candidate
			}
		}
		t.Fatalf("missing source field %s", name)
		return schema.Field{}
	}
	title, slug, author := field("title"), field("slug"), field("author")
	indexes := []schema.IndexInput{{Name: "headline_slug", Fields: []string{"headline", "slug"}, Unique: true}}
	applyN6SchemaChange(t, service, actor, schema.NewUpdateChange(schema.UpdateCollectionInput{Name: "posts", Fields: []schema.FieldInput{
		{ID: &title.ID, Name: "headline", Type: schema.TypeText, Options: &schema.FieldOptions{Pattern: "^[a-z]+$", MinLength: &minimum, MaxLength: &maximum}, Presentable: true, Help: "Renamed headline"},
		{ID: &slug.ID, Name: "slug", Type: schema.TypeText, Required: true},
		{ID: &author.ID, Name: "author", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: authors.Name, Cardinality: schema.RelationSingle, MinSelect: &relationMinimum, MaxSelect: &relationMaximum, OnDelete: schema.RelationSetNull}},
	}, Indexes: &indexes}))
	applyN6SchemaChange(t, service, actor, schema.NewCreateChange(schema.CreateCollectionInput{Name: "post_report",
		View: &schema.ViewInput{SQL: `select id,title as report_title from app.posts`}}))
	second, err := source.app.UserMigrationSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	directory := writeN6SnapshotMigrationBundle(t, before, first, second)
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.JavaScript = &JavaScriptOptions{Directory: directory}
	})
	migrations, err := fixture.app.JavaScriptMigrations()
	if err != nil || len(migrations) != 2 {
		t.Fatalf("JavaScript migrations=%d error=%v", len(migrations), err)
	}
	if _, err := NewUserMigrationRunner(fixture.app, append(migrations, migrations[0])); err == nil {
		t.Fatal("mixed manifest accepted duplicate migration ID")
	}
	rollbackProbe := errors.New("rollback snapshot probe")
	err = fixture.app.pool.InTx(context.Background(), pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := ApplyUserMigrationSnapshot(context.Background(), tx, before, first); err != nil {
			return err
		}
		return rollbackProbe
	})
	if !errors.Is(err, rollbackProbe) {
		t.Fatalf("first snapshot is not applicable: %v", err)
	}
	firstRunner, err := NewUserMigrationRunner(fixture.app, migrations[:1])
	if err != nil {
		t.Fatal(err)
	}
	if results, err := firstRunner.Up(context.Background()); err != nil || len(results) != 1 {
		t.Fatalf("first snapshot Up=%+v error=%v root=%v", results, err, n6RootCause(err))
	}
	requireN6SnapshotEquivalent(t, fixture.app, first)
	recordID := uuid.MustParse("0199ac10-0000-7000-8000-000000000120")
	if _, err := fixture.app.pool.Exec(context.Background(), `insert into app.posts(id,created_at,updated_at,title,slug) values($1,current_timestamp,current_timestamp,'preserved','one')`, recordID); err != nil {
		t.Fatal(err)
	}
	goMigration := UserMigration{ID: "202609100052_go_between", Source: []byte("n6 mixed Go migration v1"),
		Up: func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `create schema n6_mixed; create table n6_mixed.marker(value text not null); insert into n6_mixed.marker values('go')`)
			return err
		}, Down: func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `drop schema n6_mixed cascade`)
			return err
		}}
	fullRunner, err := NewUserMigrationRunner(fixture.app, []UserMigration{migrations[1], goMigration, migrations[0]})
	if err != nil {
		t.Fatal(err)
	}
	results, err := fullRunner.Up(context.Background())
	if err != nil || len(results) != 2 || results[0].Migration.ID != goMigration.ID || results[1].Migration.ID != migrations[1].ID {
		t.Fatalf("mixed sorted Up=%+v error=%v", results, err)
	}
	requireN6SnapshotEquivalent(t, fixture.app, second)
	var headline string
	if err := fixture.app.pool.QueryRow(context.Background(), `select title from app.posts where id=$1`, recordID).Scan(&headline); err != nil || headline != "preserved" {
		t.Fatalf("renamed field value=%q error=%v", headline, err)
	}

	var schemaVersionBefore int64
	if err := fixture.app.pool.QueryRow(context.Background(), `select version from pgbase_meta.schema_state where singleton`).Scan(&schemaVersionBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.app.pool.Exec(context.Background(), `delete from pgbase_meta.user_migrations where id=$1`, migrations[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fullRunner.HistorySync(context.Background(), migrations[1].ID); err != nil {
		t.Fatalf("JavaScript history-sync = %v", err)
	}
	var schemaVersionAfter, historyRows int64
	if err := fixture.app.pool.QueryRow(context.Background(), `select version,(select count(*) from pgbase_meta.user_migrations where id=$1) from pgbase_meta.schema_state where singleton`, migrations[1].ID).Scan(&schemaVersionAfter, &historyRows); err != nil {
		t.Fatal(err)
	}
	if schemaVersionAfter != schemaVersionBefore || historyRows != 1 {
		t.Fatalf("history-sync changed schema %d=>%d or rows=%d", schemaVersionBefore, schemaVersionAfter, historyRows)
	}
	if result, err := fullRunner.Down(context.Background()); err != nil || result.Migration.ID != migrations[1].ID {
		t.Fatalf("rename Down=%+v error=%v", result, err)
	}
	requireN6SnapshotEquivalent(t, fixture.app, first)
	if err := fixture.app.pool.QueryRow(context.Background(), `select title from app.posts where id=$1`, recordID).Scan(&headline); err != nil || headline != "preserved" {
		t.Fatalf("Down restored field value=%q error=%v", headline, err)
	}
}

func n6RootCause(err error) error {
	for errors.Unwrap(err) != nil {
		err = errors.Unwrap(err)
	}
	return err
}

func TestJavaScriptMigrationCancellationRollsBackAndConcurrentRunnersFence(t *testing.T) {
	t.Run("cancellation", func(t *testing.T) {
		directory := t.TempDir()
		writeN6JavaScriptFiles(t, directory, map[string]string{
			"pgbase.scripts.json": `{"format":"pgbase.scripts/v1","migrations":[{"id":"202609100060_cancel","module":"migration.js","up":"up","down":"down"}]}`,
			"migration.js":        `const pg=require("pgbase"); exports.up=function(){pg.migration.exec({sql:"create table public.n6_cancelled(value text)"});pg.migration.query({sql:"select pg_sleep(5)"});};exports.down=function(){pg.migration.exec({sql:"drop table public.n6_cancelled"});};`,
		})
		fixture := newPublicUsersAppOptions(t, false, func(options *Options) { options.JavaScript = &JavaScriptOptions{Directory: directory} })
		migrations, _ := fixture.app.JavaScriptMigrations()
		runner, err := NewUserMigrationRunner(fixture.app, migrations)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		started := time.Now()
		if _, err := runner.Up(ctx); err == nil || time.Since(started) > 2*time.Second {
			t.Fatalf("canceled Up error=%v elapsed=%s", err, time.Since(started))
		}
		var tableExists bool
		if err := fixture.app.pool.QueryRow(context.Background(), `select to_regclass('public.n6_cancelled') is not null`).Scan(&tableExists); err != nil || tableExists {
			t.Fatalf("canceled migration table=%t error=%v", tableExists, err)
		}
		history, err := runner.History(context.Background())
		if err != nil || len(history) != 1 || history[0].State != UserMigrationHistoryPending {
			t.Fatalf("canceled migration history=%+v error=%v", history, err)
		}
	})

	t.Run("concurrent fence", func(t *testing.T) {
		directory := t.TempDir()
		writeN6JavaScriptFiles(t, directory, map[string]string{
			"pgbase.scripts.json": `{"format":"pgbase.scripts/v1","migrations":[{"id":"202609100061_fence","module":"migration.js","up":"up","down":"down"}]}`,
			"migration.js":        `const pg=require("pgbase");exports.up=function(){pg.migration.exec({sql:"create table public.n6_fenced(value text unique)"});pg.migration.exec({sql:"insert into public.n6_fenced values('once')"});};exports.down=function(){pg.migration.exec({sql:"drop table public.n6_fenced"});};`,
		})
		fixture := newPublicUsersAppOptions(t, false, func(options *Options) { options.JavaScript = &JavaScriptOptions{Directory: directory} })
		migrations, _ := fixture.app.JavaScriptMigrations()
		runner, err := NewUserMigrationRunner(fixture.app, migrations)
		if err != nil {
			t.Fatal(err)
		}
		var wait sync.WaitGroup
		errorsFound := make(chan error, 2)
		appliedCounts := make(chan int, 2)
		for range 2 {
			wait.Add(1)
			go func() {
				defer wait.Done()
				results, err := runner.Up(context.Background())
				errorsFound <- err
				appliedCounts <- len(results)
			}()
		}
		wait.Wait()
		close(errorsFound)
		close(appliedCounts)
		for err := range errorsFound {
			if err != nil {
				t.Fatalf("concurrent migration error=%v root=%v", err, n6RootCause(err))
			}
		}
		totalApplied := 0
		for count := range appliedCounts {
			totalApplied += count
		}
		var rows int
		if err := fixture.app.pool.QueryRow(context.Background(), `select count(*) from public.n6_fenced`).Scan(&rows); err != nil || rows != 1 || totalApplied != 1 {
			t.Fatalf("concurrent migration rows=%d applied=%d error=%v", rows, totalApplied, err)
		}
	})
}

func applyN6SchemaChange(t *testing.T, service *schema.Service, actor uuid.UUID, change schema.Change) schema.Collection {
	t.Helper()
	plan, err := service.Preview(context.Background(), change)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Apply(context.Background(), schema.LegacyApply(change, plan, actor))
	if err != nil {
		t.Fatal(err)
	}
	return result.Collection
}

func writeN6SnapshotMigrationBundle(t *testing.T, before, first, second []byte) string {
	t.Helper()
	directory := t.TempDir()
	manifest := jsvm.Manifest{Format: jsvm.ManifestFormat, Migrations: []jsvm.Migration{
		{ID: "202609100051_create_schema", Module: "first.js", Up: "up", Down: "down", Dependencies: []string{"before.json", "first.json"}, BeforeSnapshot: before, AfterSnapshot: first},
		{ID: "202609100053_rename_schema", Module: "second.js", Up: "up", Down: "down", Dependencies: []string{"first.json", "second.json"}, BeforeSnapshot: first, AfterSnapshot: second},
	}}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeN6JavaScriptFiles(t, directory, map[string]string{
		"pgbase.scripts.json": string(manifestRaw), "before.json": string(before), "first.json": string(first), "second.json": string(second),
		"first.js":  `const pg=require("pgbase"),before=require("./before.json"),after=require("./first.json");exports.up=()=>pg.migration.applySnapshot({before,after});exports.down=()=>pg.migration.applySnapshot({before:after,after:before});`,
		"second.js": `const pg=require("pgbase"),before=require("./first.json"),after=require("./second.json");exports.up=()=>pg.migration.applySnapshot({before,after});exports.down=()=>pg.migration.applySnapshot({before:after,after:before});`,
	})
	return directory
}

func writeN6JavaScriptFiles(t *testing.T, directory string, files map[string]string) {
	t.Helper()
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func requireN6SnapshotEquivalent(t *testing.T, app *App, expected []byte) {
	t.Helper()
	actual, err := app.UserMigrationSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equivalent, err := schema.MigrationSnapshotsEquivalent(actual, expected)
	if err != nil || !equivalent {
		t.Fatalf("snapshot equivalent=%t error=%v\nactual=%s\nexpected=%s", equivalent, err, actual, expected)
	}
}
