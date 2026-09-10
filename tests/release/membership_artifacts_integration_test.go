//go:build integration && releaseartifacts

package release

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/testutil"
	"github.com/yakisoba0728/PgBase/migrations/system"
)

const (
	releaseArtifactTokenSecret = "0123456789abcdef0123456789abcdef"
	rolloutCollection          = "artifact_rollout_records"
	rolloutCollectionID        = "0199c600-0000-7000-8000-000000000001"
	rolloutEmail               = "artifact-operator@example.test"
	rolloutPassword            = "correct horse battery staple"
	rolloutRecordCount         = 300
	membershipMigrationV6Sum   = "6c3f728926d04b9bbeb81152df6eede97de155ddd2bb503a43faba4af0d6ec7a"
	membershipMigrationV7Sum   = "8c8f2a4b0506d4abaffc19097a090f84f424ef521ec9575ac4577ecaa1b3432e"
	membershipMigrationV8Sum   = "e1cf6ca6e847d1b0593c6def69c5c3a65c0916efe1011fbdacedb87f72177cff"
)

type rolloutStatus struct {
	State                string   `json:"state"`
	MinimumWriterVersion int      `json:"minimumWriterVersion"`
	CursorIssueVersion   int      `json:"cursorIssueVersion"`
	Witnesses            []string `json:"witnesses"`
}

// This is deliberately a process test. Every lease is produced by the main
// package compiled at its immutable phase commit; no test calls RegisterWriter
// and no build receives a writer-version linker value.
func TestMembershipArtifactsDriveTheRealRolloutAndRollback(t *testing.T) {
	artifacts := membershipArtifactsForProcessTest(t)
	operator := filepath.Join(artifacts, "pgbase-record-membership-v7-operator")
	databaseURL := testutil.NewDatabase(t)
	testutil.ApplySystemMigrationPrefix(t, databaseURL, migrationsThrough(t, 6))
	seedPopulatedArtifactDeployment(t, databaseURL)
	runReleaseCommandWithInput(t, artifactPath(artifacts, "a0"), databaseURL, rolloutPassword+"\n",
		"admin", "create", "--email", rolloutEmail, "--password-stdin")

	requireReleaseStartFailureWith(t, artifactPath(artifacts, "a"), databaseURL,
		"record membership is unavailable")
	a0 := startReleaseServer(t, artifactPath(artifacts, "a0"), databaseURL)
	a0.requireReady(t)
	result := runReleaseCommand(t, artifactPath(artifacts, "a0"), databaseURL,
		"migrate", "up", "--through", "7")
	requireExactMigrationReceipt(t, result, 6, membershipMigrationV6Sum, 7, membershipMigrationV7Sum)
	requireWitnessVersions(t, operator, databaseURL, 0)

	releaseA := startReleaseServer(t, artifactPath(artifacts, "a"), databaseURL)
	releaseA.requireReady(t)
	mixedA0A := requireWitnessVersions(t, operator, databaseURL, 0, 1)
	requireReleaseCommandFailureWith(t, operator, databaseURL,
		"active record writer fleet is too old",
		"membership", "cutover", "--witness", mixedA0A[0], "--witness", mixedA0A[1])
	if status := readRolloutStatus(t, operator, databaseURL); status.State != "off" {
		t.Fatalf("refused A0/A cutover moved the rollout: %#v", status)
	}
	a0.stop(t)
	expireStoppedWriter(t, databaseURL, 0)
	aWitness := requireWitnessVersions(t, operator, databaseURL, 1)[0]
	runReleaseCommand(t, operator, databaseURL, "membership", "cutover", "--witness", aWitness)
	backfill := runReleaseCommand(t, operator, databaseURL, "membership", "backfill")
	if !strings.Contains(backfill, "done=true") || strings.Count(backfill, "\n") < 3 {
		t.Fatalf("populated backfill did not run several resumable passes: %q", backfill)
	}
	runReleaseCommand(t, operator, databaseURL, "membership", "promote-ready")
	requirePopulatedBackfill(t, databaseURL)
	session := loginToRelease(t, releaseA)
	aFirstPage := requireReleaseRecordPage(t, releaseA, session, "")
	if len(aFirstPage.Items) != 100 || aFirstPage.NextCursor == "" {
		t.Fatalf("A first page = %d records cursor=%q, want 100 records and a cursor",
			len(aFirstPage.Items), aFirstPage.NextCursor)
	}
	v4Cursor := aFirstPage.NextCursor
	requireCursorVersion(t, v4Cursor, 4)

	releaseB := startReleaseServer(t, artifactPath(artifacts, "b"), databaseURL)
	releaseB.requireReady(t)
	mixed := requireWitnessVersions(t, operator, databaseURL, 1, 2)
	requireReleaseCommandFailureWith(t, operator, databaseURL,
		"active record writer fleet is too old",
		"membership", "enable-v5", "--witness", mixed[0], "--witness", mixed[1])

	releaseA.stop(t)
	expireStoppedWriter(t, databaseURL, 1)
	bWitness := requireWitnessVersions(t, operator, databaseURL, 2)[0]
	runReleaseCommand(t, operator, databaseURL, "membership", "enable-v5", "--witness", bWitness)
	enabled := readRolloutStatus(t, operator, databaseURL)
	if enabled.CursorIssueVersion != 5 || enabled.MinimumWriterVersion != 2 {
		t.Fatalf("enabled rollout = %#v", enabled)
	}
	bFromV4 := requireReleaseRecordPage(t, releaseB, session, v4Cursor)
	if len(bFromV4.Items) != 100 {
		t.Fatalf("B page resumed from A's v4 cursor = %d records, want 100", len(bFromV4.Items))
	}
	bFirstPage := requireReleaseRecordPage(t, releaseB, session, "")
	if len(bFirstPage.Items) != 100 || bFirstPage.NextCursor == "" {
		t.Fatalf("B first page = %d records cursor=%q, want 100 records and a cursor",
			len(bFirstPage.Items), bFirstPage.NextCursor)
	}
	v5Cursor := bFirstPage.NextCursor
	requireCursorVersion(t, v5Cursor, 5)

	// Release B's supported rollback lowers the floor and issuance together.
	runReleaseCommand(t, operator, databaseURL, "membership", "disable-v5")
	rolledBack := readRolloutStatus(t, operator, databaseURL)
	if rolledBack.CursorIssueVersion != 4 || rolledBack.MinimumWriterVersion != 1 {
		t.Fatalf("rolled-back rollout = %#v", rolledBack)
	}
	rejoinedA := startReleaseServer(t, artifactPath(artifacts, "a"), databaseURL)
	rejoinedA.requireReady(t)
	requireWitnessVersions(t, operator, databaseURL, 1, 2)
	aFromV5 := requireReleaseRecordPage(t, rejoinedA, session, v5Cursor)
	if len(aFromV5.Items) != 100 {
		t.Fatalf("rollback A page resumed from B's v5 cursor = %d records, want 100", len(aFromV5.Items))
	}

	rejoinedA.stop(t)
	expireStoppedWriter(t, databaseURL, 1)
	bWitness = requireWitnessVersions(t, operator, databaseURL, 2)[0]
	runReleaseCommand(t, operator, databaseURL, "membership", "enable-v5", "--witness", bWitness)

	// The stabilized B and C artifacts both recognize migration 8. Exercise the
	// database history C actually embeds so the post-retirement B rollback cannot
	// pass only because this fixture stopped at B's original v7 milestone.
	v8Receipt := runReleaseCommand(t, artifactPath(artifacts, "b"), databaseURL,
		"migrate", "up", "--through", "8")
	requireExactMigrationReceipt(t, v8Receipt, 7, membershipMigrationV7Sum, 8, membershipMigrationV8Sum)

	// Release C must fail closed before the durable v4 retirement boundary.
	// This behavior belongs to the C binary itself, so it distinguishes the
	// pinned C artifact from its dual-decoder predecessor rather than merely
	// observing a database fence that both binaries obey.
	preRetirementC := startReleaseServer(t, artifactPath(artifacts, "c"), databaseURL)
	preRetirementC.requireReady(t)
	status, body := listReleaseRecords(t, preRetirementC, session, v5Cursor)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("pre-retirement C list = %d body=%s, want 503", status, body)
	}
	requireReleaseErrorCode(t, body, "record_unavailable")
	preRetirementC.stop(t)
	expireStoppedWriter(t, databaseURL, 3)
	requireWitnessVersions(t, operator, databaseURL, 2)

	compressCursorDrainForFixture(t, databaseURL)
	runReleaseCommand(t, operator, databaseURL, "membership", "retire-v4", "--witness", bWitness)

	releaseC := startReleaseServer(t, artifactPath(artifacts, "c"), databaseURL)
	releaseC.requireReady(t)
	requireWitnessVersions(t, operator, databaseURL, 2, 3)
	v4Status, v4Body := listReleaseRecords(t, releaseC, session, v4Cursor)
	if v4Status != http.StatusBadRequest {
		t.Fatalf("C replay of retired v4 cursor = %d body=%s, want 400", v4Status, v4Body)
	}
	requireReleaseErrorCode(t, v4Body, "invalid_cursor")
	cFromV5 := requireReleaseRecordPage(t, releaseC, session, v5Cursor)
	if len(cFromV5.Items) != 100 {
		t.Fatalf("C page resumed from B's v5 cursor = %d records, want 100", len(cFromV5.Items))
	}
	if listed := countReleaseRecords(t, releaseC, session); listed != rolloutRecordCount {
		t.Fatalf("C listed %d populated records, want %d", listed, rolloutRecordCount)
	}
	releaseB.stop(t)
	expireStoppedWriter(t, databaseURL, 2)
	requireWitnessVersions(t, operator, databaseURL, 3)

	// C's documented deploy rollback is the exact dual-decoder B artifact.
	rollbackB := startReleaseServer(t, artifactPath(artifacts, "b"), databaseURL)
	rollbackB.requireReady(t)
	requireWitnessVersions(t, operator, databaseURL, 2, 3)
	rollbackV4Status, rollbackV4Body := listReleaseRecords(t, rollbackB, session, v4Cursor)
	if rollbackV4Status != http.StatusBadRequest {
		t.Fatalf("rollback B replay of retired v4 cursor = %d body=%s, want 400",
			rollbackV4Status, rollbackV4Body)
	}
	requireReleaseErrorCode(t, rollbackV4Body, "invalid_cursor")
	if page := requireReleaseRecordPage(t, rollbackB, session, v5Cursor); len(page.Items) != 100 {
		t.Fatalf("rollback B page resumed from v5 cursor with %d records, want 100", len(page.Items))
	}
	rollbackFirstPage := requireReleaseRecordPage(t, rollbackB, session, "")
	if rollbackFirstPage.NextCursor == "" {
		t.Fatal("rollback B issued no continuation cursor")
	}
	requireCursorVersion(t, rollbackFirstPage.NextCursor, 5)
	rollbackB.stop(t)
	releaseC.stop(t)
}

func migrationsThrough(t *testing.T, version int64) []system.Migration {
	t.Helper()
	all := system.Migrations()
	for index, migration := range all {
		if migration.Version == version {
			return all[:index+1]
		}
	}
	t.Fatalf("system migration %d is not embedded", version)
	return nil
}

func seedPopulatedArtifactDeployment(t *testing.T, databaseURL string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close(context.Background()) }()
	if _, err := connection.Exec(ctx, `update pgbase_meta.schema_state
		set version = 1, updated_at = transaction_timestamp() where singleton`); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `insert into pgbase_meta.collections
		(id, name, table_name, status, created_at, updated_at)
		values ($1, $2, $2, 'active', transaction_timestamp(), transaction_timestamp())`,
		rolloutCollectionID, rolloutCollection); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `create table app.`+rolloutCollection+` (
		id uuid primary key,
		created_at timestamptz not null,
		updated_at timestamptz not null)`); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `insert into app.`+rolloutCollection+` (id, created_at, updated_at)
		select gen_random_uuid(),
			transaction_timestamp() - make_interval(secs => generated.ordinal),
			transaction_timestamp()
		from generate_series(1, $1) as generated(ordinal)`, rolloutRecordCount); err != nil {
		t.Fatal(err)
	}
}

func requirePopulatedBackfill(t *testing.T, databaseURL string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close(context.Background()) }()
	var state string
	var cutoverCount, assigned, baseCount, sidecarCount int
	if err := connection.QueryRow(ctx, `select state, cutover_count, last_assigned_ordinal,
		validated_base_count, validated_sidecar_count
		from pgbase_meta.record_membership_backfills where collection_id = $1`, rolloutCollectionID).Scan(
		&state, &cutoverCount, &assigned, &baseCount, &sidecarCount,
	); err != nil {
		t.Fatal(err)
	}
	var sidecars int
	if err := connection.QueryRow(ctx, `select count(*) from pgbase_meta.record_creation_ordinals
		where collection_id = $1`, rolloutCollectionID).Scan(&sidecars); err != nil {
		t.Fatal(err)
	}
	if state != "ready" || cutoverCount != rolloutRecordCount || assigned != rolloutRecordCount ||
		baseCount != rolloutRecordCount || sidecarCount != rolloutRecordCount || sidecars != rolloutRecordCount {
		t.Fatalf("populated backfill = state %s cutover %d assigned %d validated %d/%d sidecars %d, want ready and %d",
			state, cutoverCount, assigned, baseCount, sidecarCount, sidecars, rolloutRecordCount)
	}
}

func artifactPath(directory, phase string) string {
	return filepath.Join(directory, "pgbase-record-membership-v7-"+phase)
}

type releaseServer struct {
	address  string
	command  *exec.Cmd
	finished chan struct{}
	log      *os.File
	once     sync.Once
	waitMu   sync.Mutex
	waitErr  error
}

type releaseRecordPage struct {
	Items      []json.RawMessage `json:"items"`
	NextCursor string            `json:"nextCursor"`
}

func startReleaseServer(t *testing.T, binary, databaseURL string) *releaseServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.Create(filepath.Join(t.TempDir(), "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "serve")
	command.Env = releaseEnvironment(databaseURL, address)
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	server := &releaseServer{address: address, command: command, finished: make(chan struct{}), log: logFile}
	go func() {
		err := command.Wait()
		server.waitMu.Lock()
		server.waitErr = err
		server.waitMu.Unlock()
		close(server.finished)
	}()
	t.Cleanup(func() { server.stop(t) })
	return server
}

func (server *releaseServer) requireReady(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	endpoint := "http://" + server.address + "/health/ready"
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		select {
		case <-server.finished:
			t.Fatalf("release process exited before readiness: %v\n%s", server.waitError(), server.logs())
		default:
		}
		response, err := client.Get(endpoint)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("release process did not become ready\n%s", server.logs())
}

func (server *releaseServer) baseURL() string {
	return "http://" + server.address
}

func loginToRelease(t *testing.T, server *releaseServer) *http.Cookie {
	t.Helper()
	payload, err := json.Marshal(map[string]string{
		"email": rolloutEmail, "password": rolloutPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, server.baseURL()+"/api/admin/auth/login", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", server.baseURL())
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("login to %s: %v", server.address, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login to %s = %d body=%s", server.address, response.StatusCode, body)
	}
	for _, cookie := range response.Cookies() {
		if cookie.Name == "pgbase_admin" && cookie.Value != "" {
			return cookie
		}
	}
	t.Fatalf("login to %s set no administrator session cookie: %v", server.address, response.Cookies())
	return nil
}

func listReleaseRecords(
	t *testing.T, server *releaseServer, session *http.Cookie, cursor string,
) (int, []byte) {
	t.Helper()
	query := url.Values{"limit": []string{"100"}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	target := server.baseURL() + "/api/v1/collections/" + url.PathEscape(rolloutCollection) +
		"/records?" + query.Encode()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(session)
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("list records from %s: %v", server.address, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, body
}

func requireReleaseRecordPage(
	t *testing.T, server *releaseServer, session *http.Cookie, cursor string,
) releaseRecordPage {
	t.Helper()
	status, body := listReleaseRecords(t, server, session, cursor)
	if status != http.StatusOK {
		t.Fatalf("list records from %s = %d body=%s", server.address, status, body)
	}
	var page releaseRecordPage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode list response from %s: %v body=%s", server.address, err, body)
	}
	return page
}

func requireCursorVersion(t *testing.T, token string, want int) {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) <= sha256.Size {
		t.Fatalf("decode cursor wire envelope: length=%d error=%v", len(raw), err)
	}
	var payload struct {
		Version int `json:"v"`
	}
	if err := json.Unmarshal(raw[:len(raw)-sha256.Size], &payload); err != nil {
		t.Fatalf("decode cursor wire payload: %v", err)
	}
	if payload.Version != want {
		t.Fatalf("cursor wire version = %d, want %d", payload.Version, want)
	}
}

func requireReleaseErrorCode(t *testing.T, body []byte, want string) {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode release error: %v body=%s", err, body)
	}
	if envelope.Error.Code != want {
		t.Fatalf("release error code = %q, want %q body=%s", envelope.Error.Code, want, body)
	}
}

func countReleaseRecords(t *testing.T, server *releaseServer, session *http.Cookie) int {
	t.Helper()
	total, cursor := 0, ""
	for pageNumber := 0; pageNumber < 10; pageNumber++ {
		page := requireReleaseRecordPage(t, server, session, cursor)
		total += len(page.Items)
		if page.NextCursor == "" {
			return total
		}
		cursor = page.NextCursor
	}
	t.Fatal("record listing did not reach its final page")
	return 0
}

func (server *releaseServer) stop(t *testing.T) {
	t.Helper()
	server.once.Do(func() {
		_ = server.command.Process.Signal(syscall.SIGTERM)
		select {
		case <-server.finished:
			if err := server.waitError(); err != nil {
				t.Errorf("stop release process: %v\n%s", err, server.logs())
			}
		case <-time.After(20 * time.Second):
			_ = server.command.Process.Kill()
			<-server.finished
			t.Errorf("release process did not stop after SIGTERM\n%s", server.logs())
		}
		_ = server.log.Close()
	})
}

func (server *releaseServer) waitError() error {
	server.waitMu.Lock()
	defer server.waitMu.Unlock()
	return server.waitErr
}

func (server *releaseServer) logs() string {
	_ = server.log.Sync()
	contents, _ := os.ReadFile(server.log.Name())
	return string(contents)
}

func releaseEnvironment(databaseURL, address string) []string {
	secret := base64.StdEncoding.EncodeToString([]byte(releaseArtifactTokenSecret))
	return append(os.Environ(),
		"PGBASE_DATABASE_URL="+databaseURL,
		"PGBASE_TOKEN_SECRET="+secret,
		"PGBASE_HTTP_ADDRESS="+address,
		"PGBASE_AUTO_MIGRATE=false",
		"PGBASE_SHUTDOWN_TIMEOUT=2s",
	)
}

func runReleaseCommand(t *testing.T, binary, databaseURL string, arguments ...string) string {
	t.Helper()
	output, err := tryReleaseCommand(binary, databaseURL, arguments...)
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", filepath.Base(binary), strings.Join(arguments, " "), err, output)
	}
	return output
}

func requireReleaseCommandFailureWith(
	t *testing.T, binary, databaseURL, want string, arguments ...string,
) {
	t.Helper()
	output, err := tryReleaseCommand(binary, databaseURL, arguments...)
	if err == nil || !strings.Contains(output, want) {
		t.Fatalf("%s %s error=%v output=%q, want failure containing %q",
			filepath.Base(binary), strings.Join(arguments, " "), err, output, want)
	}
}

func runReleaseCommandWithInput(
	t *testing.T, binary, databaseURL, input string, arguments ...string,
) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, arguments...)
	command.Env = releaseEnvironment(databaseURL, "127.0.0.1:0")
	command.Stdin = strings.NewReader(input)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("%s %s: %v\n%s", filepath.Base(binary), strings.Join(arguments, " "), ctx.Err(), output)
	}
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", filepath.Base(binary), strings.Join(arguments, " "), err, output)
	}
	return string(output)
}

func tryReleaseCommand(binary, databaseURL string, arguments ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, arguments...)
	command.Env = releaseEnvironment(databaseURL, "127.0.0.1:0")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if ctx.Err() != nil {
		return stdout.String() + stderr.String(), ctx.Err()
	}
	if err != nil {
		return stdout.String() + stderr.String(), err
	}
	return stdout.String(), nil
}

func readRolloutStatus(t *testing.T, operator, databaseURL string) rolloutStatus {
	t.Helper()
	output := runReleaseCommand(t, operator, databaseURL, "membership", "status", "--json")
	var status rolloutStatus
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		t.Fatalf("decode membership status %q: %v", output, err)
	}
	return status
}

func requireWitnessVersions(t *testing.T, operator, databaseURL string, versions ...int) []string {
	t.Helper()
	want := slices.Clone(versions)
	slices.Sort(want)
	deadline := time.Now().Add(30 * time.Second)
	for {
		status := readRolloutStatus(t, operator, databaseURL)
		got := make([]int, 0, len(status.Witnesses))
		for _, witness := range status.Witnesses {
			separator := strings.LastIndexByte(witness, ':')
			if separator < 0 {
				t.Fatalf("invalid membership witness %q", witness)
			}
			version, err := strconv.Atoi(witness[separator+1:])
			if err != nil {
				t.Fatalf("invalid membership witness %q: %v", witness, err)
			}
			got = append(got, version)
		}
		slices.Sort(got)
		if slices.Equal(got, want) {
			return status.Witnesses
		}
		if time.Now().After(deadline) {
			t.Fatalf("active artifact writer versions = %v from %v, want %v", got, status.Witnesses, want)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func expireStoppedWriter(t *testing.T, databaseURL string, version int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close(context.Background()) }()
	if _, err := connection.Exec(ctx, `update pgbase_meta.runtime_writer_leases
		set started_at = clock_timestamp() - interval '3 seconds',
			heartbeat_at = clock_timestamp() - interval '2 seconds',
			lease_expires_at = clock_timestamp() - interval '1 second'
		where writer_version = $1`, version); err != nil {
		t.Fatal(err)
	}
}

// The production drain remains the full 15 minutes and is already covered by
// the membership clock tests. This process test compresses only the persisted
// timestamps after issuance so it can reach the C deploy boundary in CI.
func compressCursorDrainForFixture(t *testing.T, databaseURL string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close(context.Background()) }()
	if _, err := connection.Exec(ctx, `update pgbase_meta.record_membership_capability
		set v5_enabled_at = ready_at,
			v4_decode_until = ready_at + interval '1 microsecond',
			updated_at = clock_timestamp()
		where singleton`); err != nil {
		t.Fatal(err)
	}
}
