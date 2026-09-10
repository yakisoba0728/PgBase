//go:build integration && releaseartifacts

package release

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

const (
	schemaMigrationV8Checksum = "e1cf6ca6e847d1b0593c6def69c5c3a65c0916efe1011fbdacedb87f72177cff"
	schemaMigrationV9Checksum = "19baaeab06d5b9c23333ae5c14cf0c6c806360e1dabc958c35c24263225d35c6"
)

type schemaArtifactStatus struct {
	Installed  bool                   `json:"installed"`
	State      string                 `json:"state"`
	Generation int                    `json:"generation"`
	Expected   int                    `json:"expected"`
	Valid      int                    `json:"valid"`
	Invalid    int                    `json:"invalid"`
	Mismatched int                    `json:"mismatched"`
	Missing    int                    `json:"missing"`
	Writers    []schemaArtifactWriter `json:"writers"`
}

type schemaArtifactWriter struct {
	InstanceID      string    `json:"instanceId"`
	CompilerVersion int       `json:"compilerVersion"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

type schemaArtifactPlan struct {
	BaseVersion int64    `json:"baseVersion"`
	Checksum    string   `json:"checksum"`
	SQL         []string `json:"sql"`
}

func TestSchemaCompilerArtifactsDriveP0MigrationFeatureAndDeployRollback(t *testing.T) {
	artifacts := schemaCompilerArtifactsForProcessTest(t)
	predecessorBinary := filepath.Join(artifacts, "pgbase-schema-compiler-v9-predecessor")
	p0Binary := filepath.Join(artifacts, "pgbase-schema-compiler-v9-p0")
	fBinary := filepath.Join(artifacts, "pgbase-schema-compiler-v9-f")
	databaseURL := testutil.NewDatabase(t)
	testutil.ApplySystemMigrationPrefix(t, databaseURL, migrationsThrough(t, 8))

	requireReleaseStartFailureWith(t, fBinary, databaseURL, "system migrations are not current")
	predecessor := startReleaseServer(t, predecessorBinary, databaseURL)
	predecessor.requireReady(t)
	runReleaseCommandWithInput(t, predecessorBinary, databaseURL, rolloutPassword+"\n",
		"admin", "create", "--email", rolloutEmail, "--password-stdin")
	session := loginToRelease(t, predecessor)
	seedFields := []map[string]any{{"name": "title", "type": "text", "required": true}}
	seedPlan := planSchemaArtifactChange(t, predecessor, session, "create", "artifact_predecessor_seed", seedFields)
	applySchemaArtifactChange(t, predecessor, session, "create", "artifact_predecessor_seed", seedFields, seedPlan)
	predecessorWriters := requireDatabaseSchemaWriterVersions(t, databaseURL, 2)
	predecessorWriter := predecessorWriters[0].InstanceID
	blockedPlan := planSchemaArtifactChange(t, predecessor, session, "create", "artifact_predecessor_blocked", seedFields)
	blockedBaseVersion := readSchemaArtifactCatalogVersion(t, databaseURL)

	p0 := startReleaseServer(t, p0Binary, databaseURL)
	p0.requireReady(t)
	p0Before := readSchemaArtifactStatus(t, p0Binary, databaseURL)
	if p0Before.Installed || p0Before.State != "pending" || len(p0Before.Writers) != 0 {
		t.Fatalf("P0 status on v8 = %#v", p0Before)
	}
	requireDatabaseSchemaWriterVersions(t, databaseURL, 2, 2)

	receipt := runReleaseCommand(t, p0Binary, databaseURL, "migrate", "up", "--through", "9")
	requireExactMigrationReceipt(t, receipt, 8, schemaMigrationV8Checksum, 9, schemaMigrationV9Checksum)
	p0.requireReady(t)
	status, body := requestSchemaArtifactApply(
		t, predecessor, session, "create", "artifact_predecessor_blocked", seedFields, blockedPlan,
	)
	if status == http.StatusOK || status == http.StatusCreated {
		t.Fatalf("pre-P0 process applied schema DDL after migration 9: status=%d body=%s", status, body)
	}
	requireSchemaArtifactCatalogUnchanged(
		t, databaseURL, blockedBaseVersion, "artifact_predecessor_blocked",
	)
	predecessor.stop(t)
	expireStoppedSchemaWriter(t, databaseURL, predecessorWriter)

	fields := []map[string]any{
		{"name": "title", "type": "text", "required": true, "indexed": true},
		{"name": "nickname", "type": "text", "indexed": true},
	}
	p0Plan := planSchemaArtifactChange(t, p0, session, "create", "artifact_p0_schema", fields)
	requireCompanionPlan(t, p0Plan, false)
	applySchemaArtifactChange(t, p0, session, "create", "artifact_p0_schema", fields, p0Plan)
	p0After := requireSchemaWriterVersions(t, p0Binary, databaseURL, 2)
	p0Writer := p0After.Writers[0].InstanceID
	requireReleaseCommandFailureWith(t, p0Binary, databaseURL,
		"schema companion index capability is unavailable",
		"schema-index", "activate", "--witness", p0Writer)
	if status := readSchemaArtifactStatus(t, p0Binary, databaseURL); status.State != "pending" {
		t.Fatalf("P0 activation refusal changed state: %#v", status)
	}

	feature := startReleaseServer(t, fBinary, databaseURL)
	feature.requireReady(t)
	fPendingPlan := planSchemaArtifactChange(t, feature, session, "update", "artifact_p0_schema", fields)
	requireCompanionPlan(t, fPendingPlan, false)
	applySchemaArtifactChange(t, feature, session, "update", "artifact_p0_schema", fields, fPendingPlan)
	mixedStatus := requireSchemaWriterVersions(t, fBinary, databaseURL, 2, 3)
	mixedWitnesses := make([]string, 0, len(mixedStatus.Writers))
	for _, writer := range mixedStatus.Writers {
		mixedWitnesses = append(mixedWitnesses, "--witness", writer.InstanceID)
	}
	requireReleaseCommandFailureWith(t, fBinary, databaseURL,
		"active schema writer fleet is too old",
		append([]string{"schema-index", "activate"}, mixedWitnesses...)...)
	if status := readSchemaArtifactStatus(t, fBinary, databaseURL); status.State != "pending" {
		t.Fatalf("mixed-fleet activation refusal changed state: %#v", status)
	}

	p0.stop(t)
	expireStoppedSchemaWriter(t, databaseURL, p0Writer)
	fStatus := requireSchemaWriterVersions(t, fBinary, databaseURL, 3)
	fWriter := fStatus.Writers[0].InstanceID
	runReleaseCommand(t, fBinary, databaseURL,
		"schema-index", "activate", "--witness", fWriter)
	// Migration 9 changes the durable storage state, so the exact predecessor
	// must reject it before its older physical-index checker can touch anything.
	requireReleaseCommandFailureWith(t, predecessorBinary, databaseURL,
		"invalid schema companion index capability state",
		"schema-index", "build")
	if status := readSchemaArtifactStatus(t, fBinary, databaseURL); status.State != "building" ||
		status.Expected != 1 || status.Missing != 1 || status.Valid != 0 {
		t.Fatalf("predecessor maintenance changed the F rollout: %#v", status)
	}
	runReleaseCommand(t, fBinary, databaseURL, "schema-index", "build")
	if status := readSchemaArtifactStatus(t, fBinary, databaseURL); status.State != "ready" ||
		status.Expected != 1 || status.Valid != 1 || status.Missing != 0 || status.Invalid != 0 || status.Mismatched != 0 {
		t.Fatalf("F did not promote the companion feature: %#v", status)
	}
	fReadyPlan := planSchemaArtifactChange(t, feature, session, "create", "artifact_f_schema", fields)
	requireCompanionPlan(t, fReadyPlan, true)

	// P0 is the only supported deploy rollback after migration 9. F first
	// closes the capability, then the retained P0 digest must still serve v9
	// while identifying itself as generation 2 and refusing reactivation.
	runReleaseCommand(t, fBinary, databaseURL, "schema-index", "rollback")
	feature.stop(t)
	expireStoppedSchemaWriter(t, databaseURL, fWriter)
	rollback := startReleaseServer(t, p0Binary, databaseURL)
	rollback.requireReady(t)
	rollbackPlan := planSchemaArtifactChange(t, rollback, session, "create", "artifact_rollback_schema", fields)
	requireCompanionPlan(t, rollbackPlan, false)
	applySchemaArtifactChange(t, rollback, session, "create", "artifact_rollback_schema", fields, rollbackPlan)
	rollbackStatus := requireSchemaWriterVersions(t, p0Binary, databaseURL, 2)
	requireReleaseCommandFailureWith(t, p0Binary, databaseURL,
		"schema companion index capability is unavailable",
		"schema-index", "activate", "--witness", rollbackStatus.Writers[0].InstanceID)
	if status := readSchemaArtifactStatus(t, p0Binary, databaseURL); status.State != "pending" {
		t.Fatalf("rollback P0 changed the closed capability: %#v", status)
	}
}

func planSchemaArtifactChange(
	t *testing.T, server *releaseServer, session *http.Cookie,
	operation, name string, fields []map[string]any,
) schemaArtifactPlan {
	t.Helper()
	requestBody := map[string]any{"operation": operation, "name": name, "fields": fields}
	status, body := schemaArtifactRequest(t, server, session, "/api/admin/collections/plan", requestBody)
	if status != http.StatusOK {
		t.Fatalf("plan %s %s = %d body=%s", operation, name, status, body)
	}
	var plan schemaArtifactPlan
	if err := json.Unmarshal(body, &plan); err != nil {
		t.Fatalf("decode %s plan: %v body=%s", operation, err, body)
	}
	if plan.Checksum == "" {
		t.Fatalf("%s plan has no checksum: %#v", operation, plan)
	}
	return plan
}

func applySchemaArtifactChange(
	t *testing.T, server *releaseServer, session *http.Cookie,
	operation, name string, fields []map[string]any, plan schemaArtifactPlan,
) {
	t.Helper()
	status, body := requestSchemaArtifactApply(t, server, session, operation, name, fields, plan)
	want := http.StatusOK
	if operation == "create" {
		want = http.StatusCreated
	}
	if status != want {
		t.Fatalf("apply %s %s = %d body=%s", operation, name, status, body)
	}
}

func requestSchemaArtifactApply(
	t *testing.T, server *releaseServer, session *http.Cookie,
	operation, name string, fields []map[string]any, plan schemaArtifactPlan,
) (int, []byte) {
	t.Helper()
	requestBody := map[string]any{
		"operation": operation, "name": name, "fields": fields,
		"baseVersion": plan.BaseVersion, "checksum": plan.Checksum,
	}
	return schemaArtifactRequest(t, server, session, "/api/admin/collections/apply", requestBody)
}

func schemaArtifactRequest(
	t *testing.T, server *releaseServer, session *http.Cookie, path string, payload any,
) (int, []byte) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, server.baseURL()+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", server.baseURL())
	request.Header.Set("X-PgBase-CSRF", adminauth.DeriveCSRFToken(
		[]byte(releaseArtifactTokenSecret), session.Value))
	request.AddCookie(session)
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, body
}

func requireCompanionPlan(t *testing.T, plan schemaArtifactPlan, want bool) {
	t.Helper()
	hasCompanion := strings.Contains(strings.ToLower(strings.Join(plan.SQL, "\n")), "nulls last")
	if hasCompanion != want {
		t.Fatalf("plan companion DDL = %t, want %t; SQL=%v", hasCompanion, want, plan.SQL)
	}
}

func requireReleaseStartFailureWith(t *testing.T, binary, databaseURL, want string) {
	t.Helper()
	command := exec.Command(binary, "serve")
	command.Env = releaseEnvironment(databaseURL, "127.0.0.1:0")
	output := &bytes.Buffer{}
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(output.String(), want) {
			t.Fatalf("%s startup error = %v output=%q, want %q", filepath.Base(binary), err, output, want)
		}
	case <-time.After(10 * time.Second):
		_ = command.Process.Kill()
		<-done
		t.Fatalf("%s kept serving an unsupported database\n%s", filepath.Base(binary), output)
	}
}

func requireExactMigrationReceipt(
	t *testing.T, output string, beforeVersion int, beforeChecksum string, afterVersion int, afterChecksum string,
) {
	t.Helper()
	var receipt struct {
		ReceiptVersion int `json:"receiptVersion"`
		Target         struct {
			Version  int    `json:"version"`
			Checksum string `json:"checksum"`
		} `json:"target"`
		Before struct {
			Version  int    `json:"version"`
			Checksum string `json:"checksum"`
			Dirty    bool   `json:"dirty"`
		} `json:"before"`
		After struct {
			Version  int    `json:"version"`
			Checksum string `json:"checksum"`
			Dirty    bool   `json:"dirty"`
		} `json:"after"`
	}
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		t.Fatalf("decode migration receipt %q: %v", output, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("migration receipt has trailing data: %q", output)
	}
	if receipt.ReceiptVersion != 1 || receipt.Target.Version != afterVersion ||
		receipt.Target.Checksum != afterChecksum || receipt.Before.Version != beforeVersion ||
		receipt.Before.Checksum != beforeChecksum || receipt.Before.Dirty ||
		receipt.After.Version != afterVersion || receipt.After.Checksum != afterChecksum || receipt.After.Dirty {
		t.Fatalf("migration receipt = %#v", receipt)
	}
}

func readSchemaArtifactStatus(t *testing.T, binary, databaseURL string) schemaArtifactStatus {
	t.Helper()
	output := runReleaseCommand(t, binary, databaseURL, "schema-index", "status", "--json")
	var status schemaArtifactStatus
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&status); err != nil {
		t.Fatalf("decode schema-index status %q: %v", output, err)
	}
	return status
}

func requireSchemaWriterVersions(
	t *testing.T, binary, databaseURL string, versions ...int,
) schemaArtifactStatus {
	t.Helper()
	want := slices.Clone(versions)
	slices.Sort(want)
	deadline := time.Now().Add(30 * time.Second)
	for {
		status := readSchemaArtifactStatus(t, binary, databaseURL)
		got := make([]int, len(status.Writers))
		for index, writer := range status.Writers {
			got[index] = writer.CompilerVersion
		}
		slices.Sort(got)
		if slices.Equal(got, want) {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("schema writer generations = %v, want %v", got, want)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// Migration 8 predates the generation capability, so schema-index status
// deliberately hides its lease table. This probe is limited to the artifact
// oracle that proves the two real processes registered before migration 9.
func requireDatabaseSchemaWriterVersions(
	t *testing.T, databaseURL string, versions ...int,
) []schemaArtifactWriter {
	t.Helper()
	want := slices.Clone(versions)
	slices.Sort(want)
	deadline := time.Now().Add(30 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		connection, err := pgx.Connect(ctx, databaseURL)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		rows, err := connection.Query(ctx, `select instance_id::text, compiler_version, lease_expires_at
			from pgbase_meta.schema_writer_leases
			where lease_expires_at > clock_timestamp()
			order by instance_id`)
		writers := make([]schemaArtifactWriter, 0, len(versions))
		if err == nil {
			for rows.Next() {
				var writer schemaArtifactWriter
				if err = rows.Scan(&writer.InstanceID, &writer.CompilerVersion, &writer.ExpiresAt); err != nil {
					break
				}
				writers = append(writers, writer)
			}
			if closeErr := rows.Err(); err == nil {
				err = closeErr
			}
			rows.Close()
		}
		_ = connection.Close(context.Background())
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		got := make([]int, len(writers))
		for index, writer := range writers {
			got[index] = writer.CompilerVersion
		}
		slices.Sort(got)
		if slices.Equal(got, want) {
			return writers
		}
		if time.Now().After(deadline) {
			t.Fatalf("database schema writer generations = %v, want %v", got, want)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func readSchemaArtifactCatalogVersion(t *testing.T, databaseURL string) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close(context.Background()) }()
	var version int64
	if err := connection.QueryRow(ctx, `select version from pgbase_meta.schema_state where singleton`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}

func requireSchemaArtifactCatalogUnchanged(t *testing.T, databaseURL string, version int64, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close(context.Background()) }()
	var gotVersion int64
	var catalogEntry, physicalTable bool
	if err := connection.QueryRow(ctx, `select
		(select version from pgbase_meta.schema_state where singleton),
		exists (select 1 from pgbase_meta.collections where name = $1),
		to_regclass('app.' || quote_ident($1)) is not null`, name).Scan(
		&gotVersion, &catalogEntry, &physicalTable,
	); err != nil {
		t.Fatal(err)
	}
	if gotVersion != version || catalogEntry || physicalTable {
		t.Fatalf("rejected predecessor Apply changed catalog: version=%d want=%d catalog=%t table=%t",
			gotVersion, version, catalogEntry, physicalTable)
	}
}

func expireStoppedSchemaWriter(t *testing.T, databaseURL, instanceID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close(context.Background()) }()
	if _, err := connection.Exec(ctx, `update pgbase_meta.schema_writer_leases
		set started_at = clock_timestamp() - interval '3 seconds',
			heartbeat_at = clock_timestamp() - interval '2 seconds',
			lease_expires_at = clock_timestamp() - interval '1 second'
		where instance_id = $1`, instanceID); err != nil {
		t.Fatal(err)
	}
}
