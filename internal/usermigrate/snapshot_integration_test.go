//go:build integration

package usermigrate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/doctor"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestGeneratedSnapshotMigrationsReplayRenameDownAndRollback(t *testing.T) {
	ctx := context.Background()
	source := userMigrationPool(t)
	actor := uuid.MustParse("0199ab10-0000-7000-8000-000000000100")
	insertSnapshotAdministrator(t, source, actor, "snapshot-source@example.test")
	service := schema.NewService(source, &schema.Cache{}, nil)
	before := exportSnapshot(t, source)

	authors := applySnapshotChangeForTest(t, service, actor, schema.NewCreateChange(schema.CreateCollectionInput{
		Name: "authors", Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText, Required: true, Default: json.RawMessage(`"unknown"`)}},
	}))
	posts := applySnapshotChangeForTest(t, service, actor, schema.NewCreateChange(schema.CreateCollectionInput{
		Name: "posts",
		Fields: []schema.FieldInput{
			{Name: "title", Type: schema.TypeText},
			{Name: "slug", Type: schema.TypeText},
			{Name: "author", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: authors.Name, Cardinality: schema.RelationSingle, OnDelete: schema.RelationSetNull}},
		},
		Indexes: []schema.IndexInput{{Name: "title_slug", Fields: []string{"title", "slug"}}},
	}))
	applySnapshotChangeForTest(t, service, actor, schema.NewCreateChange(schema.CreateCollectionInput{
		Name: "post_titles", View: &schema.ViewInput{Source: posts.Name, Projection: []string{"title", "slug"}},
	}))
	first := exportSnapshot(t, source)
	if again := exportSnapshot(t, source); !bytes.Equal(first, again) {
		t.Fatalf("unchanged snapshots differ:\n%s\n%s", first, again)
	}
	for _, forbidden := range [][]byte{[]byte("snapshot-source@example.test"), []byte("password_hash"), []byte("tableName"), []byte("columnName"), []byte("createdAt")} {
		if bytes.Contains(first, forbidden) {
			t.Fatalf("snapshot contains excluded value %q", forbidden)
		}
	}

	firstGenerated, err := GenerateSnapshotSource("20260909140000_initial_schema", "migrations", before, first)
	if err != nil {
		t.Fatal(err)
	}
	target := userMigrationPool(t)
	firstMigration := generatedMigration(firstGenerated, before, first)
	runner := snapshotRunner(t, target, []Migration{firstMigration})
	if results, err := runner.Up(ctx); err != nil || len(results) != 1 {
		t.Fatalf("first Up=%+v err=%v", results, err)
	}
	requireEquivalentSnapshot(t, target, first)
	if _, err := target.Exec(ctx, `insert into app.posts(id,created_at,updated_at,title,slug) values($1,current_timestamp,current_timestamp,'preserved','one')`, uuid.MustParse("0199ab10-0000-7000-8000-000000000101")); err != nil {
		t.Fatal(err)
	}

	catalog, err := service.Catalog(ctx)
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
		t.Fatalf("missing field %s", name)
		return schema.Field{}
	}
	title, slug, author := field("title"), field("slug"), field("author")
	updated := []schema.FieldInput{
		{ID: &title.ID, Name: "headline", Type: schema.TypeText, Options: &schema.FieldOptions{Pattern: "^[a-z]+$"}},
		{ID: &slug.ID, Name: "slug", Type: schema.TypeText},
		{ID: &author.ID, Name: "author", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "authors", Cardinality: schema.RelationSingle, MinSelect: intTestPointer(0), MaxSelect: intTestPointer(1), OnDelete: schema.RelationSetNull}},
	}
	indexes := []schema.IndexInput{{Name: "headline_slug", Fields: []string{"headline", "slug"}, Unique: true}}
	applySnapshotChangeForTest(t, service, actor, schema.NewUpdateChange(schema.UpdateCollectionInput{Name: "posts", Fields: updated, Indexes: &indexes}))
	applySnapshotChangeForTest(t, service, actor, schema.NewCreateChange(schema.CreateCollectionInput{
		Name: "post_report", View: &schema.ViewInput{SQL: `select id,title as report_title from app.posts`},
	}))
	second := exportSnapshot(t, source)
	secondGenerated, err := GenerateSnapshotSource("20260909150000_rename_title", "migrations", first, second)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondGenerated.Impact.Irreversible) != 0 {
		t.Fatalf("rename migration marked irreversible: %v", secondGenerated.Impact.Irreversible)
	}
	secondMigration := generatedMigration(secondGenerated, first, second)
	runner = snapshotRunner(t, target, []Migration{firstMigration, secondMigration})
	if results, err := runner.Up(ctx); err != nil || len(results) != 1 {
		var validation *schema.ValidationError
		if errors.As(err, &validation) {
			t.Fatalf("rename Up=%+v err=%v details=%+v", results, err, validation.Details)
		}
		t.Fatalf("rename Up=%+v err=%v", results, err)
	}
	requireEquivalentSnapshot(t, target, second)
	var headline string
	if err := target.QueryRow(ctx, `select title from app.posts where id=$1`, uuid.MustParse("0199ab10-0000-7000-8000-000000000101")).Scan(&headline); err != nil || headline != "preserved" {
		t.Fatalf("renamed logical field preserved physical value=%q err=%v", headline, err)
	}
	if _, err := runner.Down(ctx); err != nil {
		t.Fatal(err)
	}
	requireEquivalentSnapshot(t, target, first)
	var titleValue string
	if err := target.QueryRow(ctx, `select title from app.posts where id=$1`, uuid.MustParse("0199ab10-0000-7000-8000-000000000101")).Scan(&titleValue); err != nil || titleValue != "preserved" {
		t.Fatalf("Down renamed value=%q err=%v", titleValue, err)
	}

	failing := secondMigration
	failing.ID = "20260909150001_rollback_after_apply"
	failing.Revision = sha256.Sum256([]byte("rollback-after-apply"))
	failing.Up = func(ctx context.Context, tx pgx.Tx) error {
		if err := schema.ApplyMigrationSnapshot(ctx, tx, first, second); err != nil {
			return err
		}
		return context.Canceled
	}
	failing.Down = func(context.Context, pgx.Tx) error { return nil }
	failingRunner := snapshotRunner(t, target, []Migration{firstMigration, failing})
	if _, err := failingRunner.Up(ctx); err == nil {
		t.Fatal("expected post-apply failure")
	}
	requireEquivalentSnapshot(t, target, first)
}

func TestGeneratedSnapshotRunsFromExternalModuleAgainstFreshPostgres(t *testing.T) {
	fieldID := uuid.MustParse("0199ab10-0000-7000-8000-000000000402")
	before, err := schema.MarshalMigrationSnapshot(schema.MigrationSnapshot{Format: schema.MigrationSnapshotFormat})
	if err != nil {
		t.Fatal(err)
	}
	after, err := schema.MarshalMigrationSnapshot(schema.MigrationSnapshot{Format: schema.MigrationSnapshotFormat, Collections: []schema.MigrationSnapshotCollection{{
		Name: "notes", Fields: []schema.FieldInput{{ID: &fieldID, Name: "title", Type: schema.TypeText, Options: &schema.FieldOptions{Pattern: "^[a-z]+$"}}}, Rules: []byte(`{}`),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	generated, err := GenerateSnapshotSource("20260909130002_external_notes", "migrations", before, after)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	migrationsDirectory := filepath.Join(root, "migrations")
	if err := os.MkdirAll(migrationsDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(migrationsDirectory, generated.Filename), generated.Source, 0o644); err != nil {
		t.Fatal(err)
	}
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	goMod := "module pgbase.external.replay\n\ngo 1.26.0\n\nrequire github.com/yakisoba0728/PgBase v0.0.0\nreplace github.com/yakisoba0728/PgBase => " + repository + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	externalTest := `package replay_test

import (
	"bytes"
	"context"
	"os"
	"testing"

	_ "pgbase.external.replay/migrations"
	"github.com/yakisoba0728/PgBase/pgbase"
)

func TestGeneratedMigrationUpDown(t *testing.T) {
	ctx := context.Background()
	app, err := pgbase.New(ctx, pgbase.Options{
		DatabaseURL: os.Getenv("PGBASE_EXTERNAL_DATABASE_URL"),
		TokenSecret: []byte("01234567890123456789012345678901"),
		AutoMigrate: true,
		AllowInsecureDatabase: true,
	})
	if err != nil { t.Fatal(err) }
	defer func() { if err := app.Close(context.Background()); err != nil { t.Error(err) } }()
	runner, err := pgbase.NewUserMigrationRunner(app, pgbase.RegisteredUserMigrations())
	if err != nil { t.Fatal(err) }
	if result, err := runner.Up(ctx); err != nil || len(result) != 1 { t.Fatalf("Up=%+v err=%v", result, err) }
	snapshot, err := app.UserMigrationSnapshot(ctx)
	if err != nil || !bytes.Contains(snapshot, []byte(` + "`\"name\": \"notes\"`" + `)) { t.Fatalf("Up snapshot=%s err=%v", snapshot, err) }
	if result, err := runner.Down(ctx); err != nil || result.Migration.ID == "" { t.Fatalf("Down=%+v err=%v", result, err) }
	snapshot, err = app.UserMigrationSnapshot(ctx)
	if err != nil || bytes.Contains(snapshot, []byte(` + "`\"name\": \"notes\"`" + `)) { t.Fatalf("Down snapshot=%s err=%v", snapshot, err) }
}

`
	if err := os.WriteFile(filepath.Join(root, "replay_test.go"), []byte(externalTest), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = root
	command.Env = append(os.Environ(), "GOTOOLCHAIN=go1.26.8", "PGBASE_EXTERNAL_DATABASE_URL="+testutil.NewDatabase(t))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("external generated migration failed against PostgreSQL: %v\n%s", err, output)
	}
}

func TestGeneratedIrreversibleDownFromExternalModuleDoesNotMutatePostgres(t *testing.T) {
	fieldID := uuid.MustParse("0199ab10-0000-7000-8000-000000000403")
	empty, err := schema.MarshalMigrationSnapshot(schema.MigrationSnapshot{Format: schema.MigrationSnapshotFormat})
	if err != nil {
		t.Fatal(err)
	}
	withNotes, err := schema.MarshalMigrationSnapshot(schema.MigrationSnapshot{Format: schema.MigrationSnapshotFormat, Collections: []schema.MigrationSnapshotCollection{{
		Name: "notes", Fields: []schema.FieldInput{{ID: &fieldID, Name: "title", Type: schema.TypeText}}, Rules: []byte(`{}`),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	create, err := GenerateSnapshotSource("20260909130003_create_notes", "migrations", empty, withNotes)
	if err != nil {
		t.Fatal(err)
	}
	drop, err := GenerateSnapshotSource("20260909130004_drop_notes", "migrations", withNotes, empty)
	if err != nil {
		t.Fatal(err)
	}
	if len(drop.Impact.Irreversible) == 0 {
		t.Fatal("drop migration was not marked irreversible")
	}
	root := t.TempDir()
	migrationsDirectory := filepath.Join(root, "migrations")
	if err := os.MkdirAll(migrationsDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, generated := range []Generated{create, drop} {
		if err := os.WriteFile(filepath.Join(migrationsDirectory, generated.Filename), generated.Source, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	goMod := "module pgbase.external.irreversible\n\ngo 1.26.0\n\nrequire github.com/yakisoba0728/PgBase v0.0.0\nreplace github.com/yakisoba0728/PgBase => " + repository + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	externalTest := `package replay_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	_ "pgbase.external.irreversible/migrations"
	"github.com/yakisoba0728/PgBase/pgbase"
)

func TestIrreversibleDown(t *testing.T) {
	ctx := context.Background()
	app, err := pgbase.New(ctx, pgbase.Options{
		DatabaseURL: os.Getenv("PGBASE_EXTERNAL_DATABASE_URL"),
		TokenSecret: []byte("01234567890123456789012345678901"),
		AutoMigrate: true,
		AllowInsecureDatabase: true,
	})
	if err != nil { t.Fatal(err) }
	defer func() { if err := app.Close(context.Background()); err != nil { t.Error(err) } }()
	runner, err := pgbase.NewUserMigrationRunner(app, pgbase.RegisteredUserMigrations())
	if err != nil { t.Fatal(err) }
	if result, err := runner.Up(ctx); err != nil || len(result) != 2 { t.Fatalf("Up=%+v err=%v", result, err) }
	before, err := app.UserMigrationSnapshot(ctx)
	if err != nil { t.Fatal(err) }
	_, err = runner.Down(ctx)
	var irreversible *pgbase.IrreversibleMigrationError
	if !errors.As(err, &irreversible) { t.Fatalf("Down error=%v", err) }
	after, snapshotErr := app.UserMigrationSnapshot(ctx)
	if snapshotErr != nil || !bytes.Equal(before, after) { t.Fatalf("snapshot changed=%t err=%v", !bytes.Equal(before, after), snapshotErr) }
	history, historyErr := runner.History(ctx)
	if historyErr != nil || len(history) != 2 || history[1].State != pgbase.UserMigrationHistoryApplied { t.Fatalf("history=%+v err=%v", history, historyErr) }
}
`
	if err := os.WriteFile(filepath.Join(root, "replay_test.go"), []byte(externalTest), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = root
	command.Env = append(os.Environ(), "GOTOOLCHAIN=go1.26.8", "PGBASE_EXTERNAL_DATABASE_URL="+testutil.NewDatabase(t))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("external irreversible migration failed against PostgreSQL: %v\n%s", err, output)
	}
}

func TestHistorySyncRequiresProvenSnapshotAndIsConcurrentIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := userMigrationPool(t)
	actor := uuid.MustParse("0199ab10-0000-7000-8000-000000000300")
	insertSnapshotAdministrator(t, pool, actor, "history-sync@example.test")
	before := exportSnapshot(t, pool)
	service := schema.NewService(pool, &schema.Cache{}, nil)
	applySnapshotChangeForTest(t, service, actor, schema.NewCreateChange(schema.CreateCollectionInput{Name: "already_present", Fields: []schema.FieldInput{{Name: "value", Type: schema.TypeText}}}))
	after := exportSnapshot(t, pool)
	generated, err := GenerateSnapshotSource("20260909160000_adopt_present", "migrations", before, after)
	if err != nil {
		t.Fatal(err)
	}
	migration := generatedMigration(generated, before, after)
	runner := snapshotRunner(t, pool, []Migration{migration})
	entries, err := runner.History(ctx)
	if err != nil || len(entries) != 1 || entries[0].State != HistoryPending {
		t.Fatalf("initial history=%+v err=%v", entries, err)
	}
	var versionBefore int64
	if err := pool.QueryRow(ctx, `select version from pgbase_meta.schema_state where singleton`).Scan(&versionBefore); err != nil {
		t.Fatal(err)
	}
	withoutMetadata := migration
	withoutMetadata.BeforeSnapshot, withoutMetadata.AfterSnapshot = nil, nil
	if _, err := snapshotRunner(t, pool, []Migration{withoutMetadata}).HistorySync(ctx, withoutMetadata.ID); err == nil {
		t.Fatal("history sync accepted source without generated snapshots")
	}
	mismatchGenerated, err := GenerateSnapshotSource("20260909160001_wrong_target", "migrations", before, before)
	if err != nil {
		t.Fatal(err)
	}
	mismatch := generatedMigration(mismatchGenerated, before, before)
	if _, err := snapshotRunner(t, pool, []Migration{mismatch}).HistorySync(ctx, mismatch.ID); err == nil {
		t.Fatal("history sync accepted a mismatched target snapshot")
	}

	var wait sync.WaitGroup
	errorsFound := make(chan error, 8)
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := runner.HistorySync(ctx, migration.ID)
			errorsFound <- err
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	var versionAfter, historyCount int64
	if err := pool.QueryRow(ctx, `select version from pgbase_meta.schema_state where singleton`).Scan(&versionAfter); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_migrations where id=$1`, migration.ID).Scan(&historyCount); err != nil {
		t.Fatal(err)
	}
	if versionAfter != versionBefore || historyCount != 1 {
		t.Fatalf("history sync version %d=>%d rows=%d", versionBefore, versionAfter, historyCount)
	}
	entries, err = runner.History(ctx)
	if err != nil || entries[0].State != HistoryApplied {
		t.Fatalf("final history=%+v err=%v", entries, err)
	}
}

func TestHistoryComparisonReportsGapsMismatchesAndHistoryOnlyRows(t *testing.T) {
	ctx := context.Background()
	pool := userMigrationPool(t)
	callback := func(context.Context, pgx.Tx) error { return nil }
	oneRevision := sha256.Sum256([]byte("one"))
	twoRevision := sha256.Sum256([]byte("two"))
	manifest := []Migration{
		{ID: "20260909170000_one", Revision: oneRevision, Up: callback, Down: callback},
		{ID: "20260909170001_two", Revision: twoRevision, Up: callback, Down: callback},
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.user_migrations(id,revision,applied_at,execution_ms) values($1,$2,current_timestamp,0),($3,$4,current_timestamp,0)`,
		manifest[1].ID, twoRevision[:], "20260909170002_history_only", oneRevision[:]); err != nil {
		t.Fatal(err)
	}
	runner, err := New(pool, manifest, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := runner.History(ctx)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]HistoryState{}
	for _, entry := range entries {
		states[entry.ID] = entry.State
	}
	if states[manifest[0].ID] != HistoryPending || states[manifest[1].ID] != HistorySourceGap || states["20260909170002_history_only"] != HistoryOnly {
		t.Fatalf("history states=%v", states)
	}
	if _, err := pool.Exec(ctx, `update pgbase_meta.user_migrations set revision=$2 where id=$1`, manifest[1].ID, oneRevision[:]); err != nil {
		t.Fatal(err)
	}
	entries, err = runner.History(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.ID == manifest[1].ID && entry.State != HistoryChecksumMismatch {
			t.Fatalf("checksum mismatch state=%s", entry.State)
		}
	}
}

func generatedMigration(generated Generated, before, after []byte) Migration {
	return Migration{
		ID: generated.ID, Revision: sha256.Sum256(generated.Source), BeforeSnapshot: before, AfterSnapshot: after,
		Up: func(ctx context.Context, tx pgx.Tx) error {
			return schema.ApplyMigrationSnapshot(ctx, tx, before, after)
		},
		Down: func(ctx context.Context, tx pgx.Tx) error {
			return schema.ApplyMigrationSnapshot(ctx, tx, after, before)
		},
	}
}

func snapshotRunner(t *testing.T, pool *database.Pool, migrations []Migration) *Runner {
	t.Helper()
	validate := func(ctx context.Context, tx pgx.Tx) error {
		manifest, err := schema.ExternalMigrationManifest(ctx, tx)
		if err != nil {
			return err
		}
		if err := doctor.ValidateRequiredPhysicalSchema(ctx, tx, manifest); err == nil {
			return nil
		} else {
			for _, table := range manifest.Required().Tables {
				var kind, persistence string
				queryErr := tx.QueryRow(ctx, `select coalesce((select relkind::text from pg_class join pg_namespace on pg_namespace.oid=pg_class.relnamespace where nspname=$1 and relname=$2),''),coalesce((select relpersistence::text from pg_class join pg_namespace on pg_namespace.oid=pg_class.relnamespace where nspname=$1 and relname=$2),'')`, table.Namespace, table.Name).Scan(&kind, &persistence)
				if queryErr != nil || kind != table.Kind || persistence != table.Persistence {
					return fmt.Errorf("%w: table %s.%s kind=%q/%q want=%q/%q query=%v", err, table.Namespace, table.Name, kind, persistence, table.Kind, table.Persistence, queryErr)
				}
			}
			return err
		}
	}
	runner, err := NewWithSnapshotReader(pool, migrations, validate, nil, func(ctx context.Context, db database.DBTX) ([]byte, error) {
		return schema.ExportMigrationSnapshot(ctx, db.(pgx.Tx))
	})
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func exportSnapshot(t *testing.T, pool *database.Pool) []byte {
	t.Helper()
	var result []byte
	if err := pool.InTx(context.Background(), pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var err error
		result, err = schema.ExportMigrationSnapshot(context.Background(), tx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func requireEquivalentSnapshot(t *testing.T, pool *database.Pool, expected []byte) {
	t.Helper()
	equivalent, err := schema.MigrationSnapshotsEquivalent(exportSnapshot(t, pool), expected)
	if err != nil || !equivalent {
		t.Fatalf("snapshot equivalent=%t err=%v", equivalent, err)
	}
}

func insertSnapshotAdministrator(t *testing.T, pool *database.Pool, id uuid.UUID, email string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `insert into pgbase_meta.admins(id,email,password_hash,active,created_at,updated_at) values($1,$2,'test',true,current_timestamp,current_timestamp)`, id, email); err != nil {
		t.Fatal(err)
	}
}

func applySnapshotChangeForTest(t *testing.T, service *schema.Service, actor uuid.UUID, change schema.Change) schema.Collection {
	t.Helper()
	plan, err := service.Preview(context.Background(), change)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Apply(context.Background(), schema.LegacyApply(change, plan, actor))
	if err != nil {
		var validation *schema.ValidationError
		if errors.As(err, &validation) {
			t.Fatalf("apply schema change: %v details=%+v", err, validation.Details)
		}
		t.Fatal(err)
	}
	return result.Collection
}

func intTestPointer(value int) *int { return &value }
