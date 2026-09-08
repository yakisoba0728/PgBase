//go:build restore

package restore

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	administratorEmail    = "restore@example.test"
	administratorPassword = "restore-drill-administrator-password"
	peopleCollection      = "restore_people"
	articleCollection     = "restore_articles"
	publicCollection      = "restore_public_notes"
	publicOwnerEmail      = "restore-owner@example.test"
	publicForeignEmail    = "restore-foreign@example.test"
	publicUserPassword    = "restore-public-user-password"
	articleIdempotencyKey = "restore-article-record"
)

// TestRestoreDrill is the whole backup/restore procedure: seed a project through
// the real wire contract, dump it, restore it into an empty database, and prove
// the restored project is operable rather than merely present.
func TestRestoreDrill(t *testing.T) {
	drill := newCandidate(t)
	if drill.release && os.Getenv("PGBASE_RESTORE_POSTGRES_IMAGES") != "" {
		t.Fatal("a release drill must not narrow the PostgreSQL matrix")
	}
	t.Logf("restore drill runs the %s", drill.describe())
	for _, image := range postgresMatrix() {
		t.Run("postgresql-"+image.major, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			postgres := startPostgres(t, ctx, drill, image)
			drill.fileDirectory = t.TempDir()
			if err := os.Chmod(drill.fileDirectory, 0777); err != nil {
				t.Fatal(err)
			}
			expected := seedSourceProject(t, ctx, drill, postgres)
			sentinel := insertReconciliationSentinel(t, ctx, postgres)
			dumpAndRestore(t, ctx, postgres)
			restoredFiles := filepath.Join(t.TempDir(), "files")
			if err := os.CopyFS(restoredFiles, os.DirFS(drill.fileDirectory)); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(restoredFiles, 0777); err != nil {
				t.Fatal(err)
			}
			drill.fileDirectory = restoredFiles
			verifyRestoredDatabase(t, ctx, postgres, expected)
			verifyRestoredDoctor(t, ctx, drill, postgres)
			verifyRestoredService(t, ctx, drill, postgres, expected, sentinel)
		})
	}
}

// TestReleaseModeRefusesAnythingButADigestPinnedCandidate keeps the release
// contract honest without a registry: a release drill must never silently fall
// back to a source-built binary or a mutable tag.
func TestReleaseModeRefusesAnythingButADigestPinnedCandidate(t *testing.T) {
	digest := "sha256:" + strings.Repeat("ab", 32)
	tests := []struct {
		name        string
		image       string
		version     string
		wantRelease bool
		wantError   bool
	}{
		{name: "local mode sets neither variable", wantRelease: false},
		{name: "image without version", image: "ghcr.io/webisnoteasy/pgbase@" + digest, wantError: true},
		{name: "version without image", version: "v1.2.3", wantError: true},
		{name: "mutable tag", image: "ghcr.io/webisnoteasy/pgbase:v1.2.3", version: "v1.2.3", wantError: true},
		{name: "tag and digest", image: "ghcr.io/webisnoteasy/pgbase:v1.2.3@" + digest, version: "v1.2.3", wantError: true},
		{name: "bare repository", image: "ghcr.io/webisnoteasy/pgbase", version: "v1.2.3", wantError: true},
		{name: "truncated digest", image: "ghcr.io/webisnoteasy/pgbase@sha256:abc", version: "v1.2.3", wantError: true},
		{name: "uncanonical version", image: "ghcr.io/webisnoteasy/pgbase@" + digest, version: "1.2.3", wantError: true},
		{name: "unsupported channel", image: "ghcr.io/webisnoteasy/pgbase@" + digest, version: "v1.2.3-nightly.1", wantError: true},
		{name: "digest pinned release", image: "ghcr.io/webisnoteasy/pgbase@" + digest, version: "v1.2.3-rc.1", wantRelease: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			release, err := resolveCandidateMode(test.image, test.version)
			if test.wantError {
				if err == nil {
					t.Fatalf("release mode accepted image %q version %q", test.image, test.version)
				}
				if release {
					t.Error("a rejected candidate must not select release mode")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
			if release != test.wantRelease {
				t.Fatalf("release = %v, want %v", release, test.wantRelease)
			}
		})
	}
}

func TestDefaultPostgresMatrixCoversEverySupportedMajor(t *testing.T) {
	t.Setenv("PGBASE_RESTORE_POSTGRES_IMAGES", "")
	covered := make(map[string]string)
	for _, image := range postgresMatrix() {
		if !strings.Contains(image.reference, "@sha256:") {
			t.Errorf("PostgreSQL %s image %q is not digest pinned", image.major, image.reference)
		}
		if previous, duplicate := covered[image.major]; duplicate {
			t.Errorf("PostgreSQL %s is covered twice: %q and %q", image.major, previous, image.reference)
		}
		covered[image.major] = image.reference
	}
	for _, major := range supportedPostgresMajors {
		if _, ok := covered[major]; !ok {
			t.Errorf("the restore drill does not cover PostgreSQL %s", major)
		}
	}
	if len(covered) != len(supportedPostgresMajors) {
		t.Errorf("restore drill majors = %v, want %v", covered, supportedPostgresMajors)
	}
}

// sourceProject records everything the drill must still observe after the dump
// has crossed an empty database.
type sourceProject struct {
	ada             record
	bob             record
	article         record
	articleETag     string
	systemMigration int
	revisions       int
	auditEvents     int
	outboxEvents    int
	idempotency     int
	schemaReceipts  int
	publicOwner     publicUser
	publicForeign   publicUser
	publicRecord    record
	users           int
	owners          int
	fileID          string
}

func seedSourceProject(t *testing.T, ctx context.Context, drill *candidate, postgres *postgresServer) sourceProject {
	t.Helper()
	sourceURL := drill.databaseURL(postgres, sourceDatabase)
	if output, err := drill.command(ctx, sourceURL, "", "migrate", "up"); err != nil {
		t.Fatalf("migrate the source database: %v\n%s", err, output)
	}
	if output, err := drill.command(ctx, sourceURL, administratorPassword+"\n",
		"admin", "create", "--email", administratorEmail, "--password-stdin"); err != nil {
		t.Fatalf("create the drill administrator: %v\n%s", err, output)
	}

	instance := drill.serve(t, ctx, sourceURL, "pgbase-restore-source-"+randomHex(t, 6))
	client := newAdminClient(instance.baseURL)
	client.login(t, ctx, administratorEmail, administratorPassword)
	if drill.release {
		if reported := client.reportedVersion(t, ctx); reported != drill.version {
			t.Fatalf("candidate reported version %q, want the expected release version %q", reported, drill.version)
		}
	}

	client.applyCollection(t, ctx, map[string]any{
		"name": peopleCollection,
		"fields": []map[string]any{
			{"name": "name", "type": "text", "required": true, "unique": false, "indexed": true},
		},
	})
	client.applyCollection(t, ctx, map[string]any{"name": publicCollection, "fields": []map[string]any{{"name": "title", "type": "text", "required": true, "unique": false, "indexed": true}}})
	client.call(t, ctx, http.MethodPut, "/api/admin/collections/"+publicCollection+"/user-access", map[string]any{"enabled": true}, nil, http.StatusOK, nil)
	client.applyCollection(t, ctx, map[string]any{
		"name": articleCollection,
		"fields": []map[string]any{
			{"name": "title", "type": "text", "required": true, "unique": false, "indexed": false},
			{"name": "owner", "type": "relation", "required": false, "unique": false, "indexed": true,
				"relation": map[string]any{"targetCollection": peopleCollection, "cardinality": "single", "onDelete": "restrict"}},
			{"name": "reviewers", "type": "relation", "required": false, "unique": false, "indexed": true,
				"relation": map[string]any{"targetCollection": peopleCollection, "cardinality": "multiple", "maxSelect": 4, "onDelete": "restrict"}},
		},
	})

	project := sourceProject{}
	project.ada = client.createRecord(t, ctx, peopleCollection, "restore-person-ada", map[string]any{"name": "Ada"})
	project.bob = client.createRecord(t, ctx, peopleCollection, "restore-person-bob", map[string]any{"name": "Bob"})
	project.article = client.createRecord(t, ctx, articleCollection, articleIdempotencyKey, map[string]any{
		"title":     "restore-relation-sentinel",
		"owner":     project.ada.id,
		"reviewers": []string{project.bob.id, project.ada.id},
	})
	// A second revision proves the sidecar, audit trail, and outbox survive the
	// dump with more than their creation row.
	project.articleETag = client.patchRecord(t, ctx, articleCollection, project.article.id, project.article.etag,
		map[string]any{"title": "restore-relation-sentinel-revised"})
	if project.articleETag == "" || project.articleETag == project.article.etag {
		t.Fatalf("PATCH ETag = %q, want a new strong validator", project.articleETag)
	}
	project.publicOwner = registerPublicUser(t, ctx, instance.baseURL, publicOwnerEmail, publicUserPassword)
	project.publicForeign = registerPublicUser(t, ctx, instance.baseURL, publicForeignEmail, publicUserPassword)
	var created map[string]any
	publicRecordRequest(t, ctx, instance.baseURL, project.publicOwner.Token, http.MethodPost, "/api/v1/user/collections/"+publicCollection+"/records", map[string]any{"title": "restore-owner-note"}, http.StatusCreated, &created)
	identifier, _ := created["id"].(string)
	if identifier == "" {
		t.Fatalf("public record has no id: %#v", created)
	}
	project.publicRecord = record{id: identifier}

	if version := mustAtoi(t, postgres.query(t, ctx, sourceDatabase, "select max(version) from pgbase_meta.system_migrations")); version >= 13 {
		project.fileID = seedRestoreFile(t, ctx, client)
	}
	// The source instance must be drained before the dump so the snapshot never
	// races the schema-operation maintenance loop.
	instance.stop(t)

	// The release gate harness can be newer than an immutable rollout phase.
	// Record what the candidate actually migrated rather than assuming its
	// embedded history has the harness checkout's current length.
	project.systemMigration = mustAtoi(t, postgres.query(t, ctx, sourceDatabase,
		"select count(*) from pgbase_meta.system_migrations"))
	project.revisions = mustAtoi(t, postgres.query(t, ctx, sourceDatabase, "select count(*) from pgbase_meta.record_revisions"))
	project.auditEvents = mustAtoi(t, postgres.query(t, ctx, sourceDatabase, "select count(*) from pgbase_meta.record_audit_events"))
	project.outboxEvents = mustAtoi(t, postgres.query(t, ctx, sourceDatabase, "select count(*) from pgbase_meta.record_outbox"))
	project.idempotency = mustAtoi(t, postgres.query(t, ctx, sourceDatabase, "select count(*) from pgbase_meta.record_idempotency"))
	project.schemaReceipts = mustAtoi(t, postgres.query(t, ctx, sourceDatabase, "select count(*) from pgbase_meta.schema_migrations"))
	project.users = mustAtoi(t, postgres.query(t, ctx, sourceDatabase, "select count(*) from pgbase_meta.users"))
	project.owners = mustAtoi(t, postgres.query(t, ctx, sourceDatabase, "select count(*) from pgbase_meta.record_owners"))
	for name, count := range map[string]int{
		"record revisions":   project.revisions,
		"record audit":       project.auditEvents,
		"record outbox":      project.outboxEvents,
		"record idempotency": project.idempotency,
		"schema receipts":    project.schemaReceipts,
	} {
		if count == 0 {
			t.Fatalf("the source project produced no %s rows to restore", name)
		}
	}
	return project
}

// insertReconciliationSentinel leaves a schema operation whose outcome the
// source instance never learned. A restored project must reconcile it instead
// of carrying an eternally running claim.
func insertReconciliationSentinel(t *testing.T, ctx context.Context, postgres *postgresServer) string {
	t.Helper()
	operationID := postgres.query(t, ctx, sourceDatabase, `
		INSERT INTO pgbase_meta.schema_operations (
			operation_id, mode, actor_kind, actor_id, resource_kind, resource_id,
			resource_key_hash, operation, idempotency_key_hash, request_hash,
			plan_checksum, base_version, status, fence_generation, lease_owner,
			lease_expires_at, reconcile_after, created_at, updated_at
		)
		SELECT
			gen_random_uuid(), 'legacy', 'administrator', admins.id, 'collection', gen_random_uuid(),
			decode(repeat('22',32),'hex'), 'collection_create', NULL, decode(repeat('33',32),'hex'),
			decode(repeat('44',32),'hex'),
			(SELECT version FROM pgbase_meta.schema_state WHERE singleton),
			'running', 1, gen_random_uuid(),
			current_timestamp - interval '4 minutes',
			current_timestamp - interval '3 minutes 55 seconds',
			current_timestamp - interval '10 minutes',
			current_timestamp - interval '5 minutes'
		FROM pgbase_meta.admins admins
		WHERE admins.email = '`+administratorEmail+`'
		RETURNING operation_id`)
	// psql prints the RETURNING row and then the INSERT command tag.
	operationID, _, _ = strings.Cut(operationID, "\n")
	if strings.TrimSpace(operationID) == "" {
		t.Fatal("the reconciliation sentinel was not inserted")
	}
	return strings.TrimSpace(operationID)
}

func dumpAndRestore(t *testing.T, ctx context.Context, postgres *postgresServer) {
	t.Helper()
	// A newer client can emit settings an older server rejects, so the drill
	// asserts the documented client/server major match before dumping.
	clientVersion, err := postgres.exec(ctx, "pg_restore", "--version")
	if err != nil {
		t.Fatalf("read the pg_restore version: %v\n%s", err, clientVersion)
	}
	clientMajor, _, _ := strings.Cut(strings.Fields(strings.TrimSpace(clientVersion))[2], ".")
	serverVersionNumber := mustAtoi(t, postgres.query(t, ctx, sourceDatabase, "show server_version_num"))
	if serverMajor := serverVersionNumber / 10000; clientMajor != strconv.Itoa(serverMajor) {
		t.Fatalf("pg_restore client major %s does not match server major %d", clientMajor, serverMajor)
	}

	if output, err := postgres.exec(ctx, "bash", "-c",
		"pg_dump --username "+databaseUser+" --dbname "+sourceDatabase+
			" --format=custom --no-owner --no-privileges --file /tmp/pgbase.dump"); err != nil {
		t.Fatalf("dump the source database: %v\n%s", err, output)
	}
	// The dump has to leave the database host to be a backup at all, so the
	// drill round-trips the artifact through the test machine.
	artifact := filepath.Join(t.TempDir(), "pgbase.dump")
	if output, err := dockerRun(ctx, 5*time.Minute, "cp", postgres.container+":/tmp/pgbase.dump", artifact); err != nil {
		t.Fatalf("copy the dump off the database host: %v\n%s", err, output)
	}
	info, err := os.Stat(artifact)
	if err != nil || info.Size() == 0 {
		t.Fatalf("the dump artifact is missing or empty: %v", err)
	}
	if output, err := dockerRun(ctx, 5*time.Minute, "cp", artifact, postgres.container+":/tmp/pgbase-restore.dump"); err != nil {
		t.Fatalf("stage the dump for restore: %v\n%s", err, output)
	}

	if output, err := postgres.exec(ctx, "createdb", "--username", databaseUser, restoredDatabase); err != nil {
		t.Fatalf("create the empty restore target: %v\n%s", err, output)
	}
	if output, err := postgres.exec(ctx, "pg_restore",
		"--username="+databaseUser, "--exit-on-error", "--no-owner", "--no-privileges",
		"--single-transaction", "--dbname="+restoredDatabase, "/tmp/pgbase-restore.dump"); err != nil {
		t.Fatalf("restore into the empty database: %v\n%s", err, output)
	}
}

func verifyRestoredDatabase(t *testing.T, ctx context.Context, postgres *postgresServer, expected sourceProject) {
	t.Helper()
	for _, schema := range []string{"pgbase_meta", "app", "pgbase_relation"} {
		if got := postgres.query(t, ctx, restoredDatabase,
			"select count(*) from information_schema.schemata where schema_name = '"+schema+"'"); got != "1" {
			t.Errorf("restored database is missing the %s schema", schema)
		}
	}
	assertCount(t, postgres, ctx, "system migrations", "select count(*) from pgbase_meta.system_migrations", expected.systemMigration)
	assertCount(t, postgres, ctx, "administrators", "select count(*) from pgbase_meta.admins", 1)
	expectedCollections := 3
	if expected.fileID != "" {
		expectedCollections++
	}
	assertCount(t, postgres, ctx, "collections", "select count(*) from pgbase_meta.collections", expectedCollections)
	assertCount(t, postgres, ctx, "field relations", "select count(*) from pgbase_meta.field_relations", 2)
	assertCount(t, postgres, ctx, "people", "select count(*) from app."+peopleCollection, 2)
	assertCount(t, postgres, ctx, "articles", "select count(*) from app."+articleCollection, 1)
	assertCount(t, postgres, ctx, "public notes", "select count(*) from app."+publicCollection, 1)
	assertCount(t, postgres, ctx, "public users", "select count(*) from pgbase_meta.users", expected.users)
	assertCount(t, postgres, ctx, "record owners", "select count(*) from pgbase_meta.record_owners", expected.owners)
	assertCount(t, postgres, ctx, "record revisions", "select count(*) from pgbase_meta.record_revisions", expected.revisions)
	assertCount(t, postgres, ctx, "record audit events", "select count(*) from pgbase_meta.record_audit_events", expected.auditEvents)
	assertCount(t, postgres, ctx, "record outbox events", "select count(*) from pgbase_meta.record_outbox", expected.outboxEvents)
	assertCount(t, postgres, ctx, "record idempotency results", "select count(*) from pgbase_meta.record_idempotency", expected.idempotency)
	assertCount(t, postgres, ctx, "schema receipts", "select count(*) from pgbase_meta.schema_migrations", expected.schemaReceipts)

	if owner := postgres.query(t, ctx, restoredDatabase,
		"select people.name from app."+articleCollection+" articles join app."+peopleCollection+
			" people on people.id = articles.owner"); owner != "Ada" {
		t.Errorf("restored single relation owner = %q, want Ada", owner)
	}
	edgeTable := postgres.query(t, ctx, restoredDatabase, `select format('%I.%I', 'pgbase_relation', relations.storage_name)
		from pgbase_meta.field_relations relations
		join pgbase_meta.fields fields on fields.id = relations.field_id
		join pgbase_meta.collections collections on collections.id = relations.source_collection_id
		where collections.name = '`+articleCollection+`' and fields.name = 'reviewers' and relations.cardinality = 'multiple'`)
	if !strings.HasPrefix(edgeTable, "pgbase_relation.") {
		t.Fatalf("restored multiple relation edge table = %q", edgeTable)
	}
	if order := postgres.query(t, ctx, restoredDatabase,
		"select string_agg(people.name, ',' order by edges.position) from "+edgeTable+
			" edges join app."+peopleCollection+" people on people.id = edges.target_id"); order != "Bob,Ada" {
		t.Errorf("restored multiple relation order = %q, want Bob,Ada", order)
	}

	// The revision sidecar must line up with the live record, not merely exist.
	if sidecar := postgres.query(t, ctx, restoredDatabase,
		"select count(*) from pgbase_meta.record_revisions revisions join app."+articleCollection+
			" articles on articles.id = revisions.record_id"); sidecar != "1" {
		t.Errorf("restored article revision sidecar rows = %s, want 1", sidecar)
	}
	// Each applied collection left a committed ledger entry whose receipt is
	// the deferred foreign key into the migration history.
	if committed := postgres.query(t, ctx, restoredDatabase, `select count(*)
		from pgbase_meta.schema_operations operations
		join pgbase_meta.schema_migrations migrations
		  on migrations.version = operations.committed_version
		 and migrations.operation_id = operations.operation_id
		 and migrations.operation = operations.operation
		 and migrations.checksum = operations.plan_checksum
		 and migrations.applied_by = operations.actor_id
		where operations.status = 'committed'`); committed != strconv.Itoa(expectedCollections) {
		t.Errorf("restored committed schema operation receipts = %s, want %d", committed, expectedCollections)
	}
	if unresolved := postgres.query(t, ctx, restoredDatabase,
		"select count(*) from pgbase_meta.schema_operations where status in ('running','unknown')"); unresolved != "1" {
		t.Errorf("restored unresolved schema operations = %s, want the single sentinel", unresolved)
	}
}

func verifyRestoredDoctor(t *testing.T, ctx context.Context, drill *candidate, postgres *postgresServer) {
	t.Helper()
	output, err := drill.command(ctx, drill.databaseURL(postgres, restoredDatabase), "", "doctor", "--json")
	if err != nil {
		t.Fatalf("run doctor against the restored database: %v\n%s", err, output)
	}
	var report struct {
		OK       bool              `json:"ok"`
		Findings []json.RawMessage `json:"findings"`
	}
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode the doctor report: %v\n%s", err, output)
	}
	if !report.OK || len(report.Findings) != 0 {
		t.Fatalf("doctor reported ok=%v with %d findings\n%s", report.OK, len(report.Findings), output)
	}
}

func verifyRestoredService(t *testing.T, ctx context.Context, drill *candidate, postgres *postgresServer, expected sourceProject, sentinel string) {
	t.Helper()
	restoredURL := drill.databaseURL(postgres, restoredDatabase)
	instance := drill.serve(t, ctx, restoredURL, "pgbase-restore-target-"+randomHex(t, 6))
	client := newAdminClient(instance.baseURL)
	client.login(t, ctx, administratorEmail, administratorPassword)
	if expected.fileID != "" {
		verifyRestoreFile(t, ctx, client, expected.fileID)
	}
	if drill.release {
		if reported := client.reportedVersion(t, ctx); reported != drill.version {
			t.Fatalf("restored candidate reported version %q, want %q", reported, drill.version)
		}
	}

	// The strong validator has to survive the dump, or every conditional client
	// silently loses its stale-write protection after a restore.
	getHeaders := client.call(t, ctx, http.MethodGet,
		recordsPath(articleCollection)+"/"+expected.article.id, nil, nil, http.StatusOK, nil)
	if got := getHeaders.Get("ETag"); got != expected.articleETag {
		t.Errorf("restored record ETag = %q, want %q", got, expected.articleETag)
	}

	// Replaying the original key must return the original result rather than
	// creating a duplicate, which only holds if the idempotency rows restored.
	replayHeaders := make(http.Header)
	replayHeaders.Set("Idempotency-Key", articleIdempotencyKey)
	var replayed map[string]any
	responseHeaders := client.call(t, ctx, http.MethodPost, recordsPath(articleCollection), map[string]any{
		"title":     "restore-relation-sentinel",
		"owner":     expected.ada.id,
		"reviewers": []string{expected.bob.id, expected.ada.id},
	}, replayHeaders, http.StatusCreated, &replayed)
	if responseHeaders.Get("Idempotency-Replayed") != "true" {
		t.Errorf("post-restore replay Idempotency-Replayed = %q, want true", responseHeaders.Get("Idempotency-Replayed"))
	}
	if identifier, _ := replayed["id"].(string); identifier != expected.article.id {
		t.Errorf("post-restore replay id = %q, want %q", identifier, expected.article.id)
	}
	if responseHeaders.Get("ETag") != expected.article.etag {
		t.Errorf("post-restore replay ETag = %q, want the original create ETag %q",
			responseHeaders.Get("ETag"), expected.article.etag)
	}
	if responseHeaders.Get("Location") != expected.article.location {
		t.Errorf("post-restore replay Location = %q, want %q", responseHeaders.Get("Location"), expected.article.location)
	}
	assertCount(t, postgres, ctx, "articles after replay", "select count(*) from app."+articleCollection, 1)
	for _, expectedUser := range []publicUser{expected.publicOwner, expected.publicForeign} {
		var session struct {
			User struct {
				ID string `json:"id"`
			} `json:"user"`
		}
		publicRecordRequest(t, ctx, instance.baseURL, expectedUser.Token, http.MethodGet, "/api/v1/auth/session", nil, http.StatusOK, &session)
		if session.User.ID != expectedUser.ID {
			t.Fatalf("restored session user = %q, want %q", session.User.ID, expectedUser.ID)
		}
	}
	owner := loginPublicUser(t, ctx, instance.baseURL, publicOwnerEmail, publicUserPassword)
	foreign := loginPublicUser(t, ctx, instance.baseURL, publicForeignEmail, publicUserPassword)
	var ownerPage struct {
		Items []map[string]any `json:"items"`
	}
	publicRecordRequest(t, ctx, instance.baseURL, owner.Token, http.MethodGet, "/api/v1/user/collections/"+publicCollection+"/records", nil, http.StatusOK, &ownerPage)
	if len(ownerPage.Items) != 1 || ownerPage.Items[0]["id"] != expected.publicRecord.id {
		t.Fatalf("restored owner list = %#v", ownerPage.Items)
	}
	publicRecordRequest(t, ctx, instance.baseURL, owner.Token, http.MethodGet, "/api/v1/user/collections/"+publicCollection+"/records/"+expected.publicRecord.id, nil, http.StatusOK, nil)
	var foreignPage struct {
		Items []map[string]any `json:"items"`
	}
	publicRecordRequest(t, ctx, instance.baseURL, foreign.Token, http.MethodGet, "/api/v1/user/collections/"+publicCollection+"/records", nil, http.StatusOK, &foreignPage)
	if len(foreignPage.Items) != 0 {
		t.Fatalf("restored foreign list = %#v", foreignPage.Items)
	}
	publicRecordRequest(t, ctx, instance.baseURL, foreign.Token, http.MethodGet, "/api/v1/user/collections/"+publicCollection+"/records/"+expected.publicRecord.id, nil, http.StatusNotFound, nil)

	// The restored instance owns the unresolved claim now, so it must close it.
	deadline := time.Now().Add(90 * time.Second)
	for {
		status := postgres.query(t, ctx, restoredDatabase,
			"select status || ':' || coalesce(failure_code, '') from pgbase_meta.schema_operations where operation_id = '"+sentinel+"'")
		if status == "failed:reconcile_failed" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the running schema operation sentinel stayed at %q after restore\n%s", status, instance.diagnostics())
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func assertCount(t *testing.T, postgres *postgresServer, ctx context.Context, name, sql string, want int) {
	t.Helper()
	if got := postgres.query(t, ctx, restoredDatabase, sql); got != strconv.Itoa(want) {
		t.Errorf("restored %s = %s, want %d", name, got, want)
	}
}
