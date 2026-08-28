//go:build e2e

package e2e

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type e2eSuite struct {
	repoRoot      string
	composeFile   string
	project       string
	image         string
	baseURL       string
	httpPort      int
	postgresPort  int
	setupToken    string
	tokenSecret   string
	databasePass  string
	administrator string
	password      string
	version       string
	environment   []string
}

var suite e2eSuite
var collectionSequence atomic.Uint64

func uniqueCollection(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, collectionSequence.Add(1))
}

func TestMain(m *testing.M) {
	configured, err := configureSuite()
	if err != nil {
		fmt.Fprintln(os.Stderr, "configure e2e suite:", err)
		os.Exit(1)
	}
	suite = configured
	if output, err := suite.compose(context.Background(), "up", "-d", "--build"); err != nil {
		fmt.Fprintf(os.Stderr, "start e2e compose project: %v\n%s\n", err, output)
		suite.writeFailureLogs()
		if cleanupErr := suite.cleanup(); cleanupErr != nil {
			fmt.Fprintln(os.Stderr, "clean failed e2e startup:", cleanupErr)
		}
		os.Exit(1)
	}
	code := m.Run()
	if code != 0 {
		suite.writeFailureLogs()
	}
	if cleanupErr := suite.cleanup(); cleanupErr != nil {
		fmt.Fprintln(os.Stderr, "clean e2e resources:", cleanupErr)
		code = 1
	}
	os.Exit(code)
}

func TestBuiltImageReportsInjectedVersion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	waitReady(t, ctx, suite.baseURL)
	client := authenticatedClient(t, ctx, suite.baseURL)
	var status struct {
		Version string `json:"version"`
	}
	if err := client.request(ctx, http.MethodGet, "/api/admin/system", nil, http.StatusOK, &status); err != nil {
		t.Fatal(err)
	}
	if status.Version != suite.version {
		t.Fatalf("system version = %q, want injected build version %q", status.Version, suite.version)
	}
}

func TestReadinessTracksDatabaseOutageAndRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	waitReady(t, ctx, suite.baseURL)
	if output, err := suite.compose(ctx, "pause", "postgres"); err != nil {
		t.Fatalf("pause PostgreSQL: %v\n%s", err, output)
	}
	paused := true
	t.Cleanup(func() {
		if !paused {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		if output, err := suite.compose(cleanupCtx, "unpause", "postgres"); err != nil {
			t.Errorf("unpause PostgreSQL during cleanup: %v\n%s", err, output)
		}
	})

	client := newAPIClient(suite.baseURL)
	deadline := time.Now().Add(10 * time.Second)
	var unavailable error
	for time.Now().Before(deadline) {
		requestCtx, requestCancel := context.WithTimeout(ctx, 3*time.Second)
		unavailable = client.request(requestCtx, http.MethodGet, "/health/ready", nil, http.StatusServiceUnavailable, nil)
		requestCancel()
		if unavailable == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if unavailable != nil {
		t.Fatalf("readiness did not report PostgreSQL outage: %v", unavailable)
	}
	if output, err := suite.compose(ctx, "unpause", "postgres"); err != nil {
		t.Fatalf("unpause PostgreSQL: %v\n%s", err, output)
	}
	paused = false
	waitReady(t, ctx, suite.baseURL)
}

func TestTwoInstancesSynchronizeSchema(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	waitReady(t, ctx, suite.baseURL)
	instanceA := authenticatedClient(t, ctx, suite.baseURL)

	instanceBURL, instanceBName := suite.startInstance(t, ctx, "schema-b", 12*time.Second)
	t.Cleanup(func() { suite.removeContainer(t, instanceBName) })
	waitReady(t, ctx, instanceBURL)
	instanceB := authenticatedClient(t, ctx, instanceBURL)

	collectionName := uniqueCollection("shared_notes")
	definition := map[string]any{
		"name": collectionName,
		"fields": []map[string]any{
			{"name": "body", "type": "text", "required": true, "unique": false, "indexed": false},
		},
	}
	applyCollection(t, ctx, instanceA, definition)

	deadline := time.Now().Add(15 * time.Second)
	for {
		var catalog struct {
			Collections map[string]json.RawMessage `json:"collections"`
		}
		err := instanceB.request(ctx, http.MethodGet, "/api/admin/collections", nil, http.StatusOK, &catalog)
		if err == nil {
			if _, ok := catalog.Collections[collectionName]; ok {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("instance B did not observe schema applied through A: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	record := createRecord(t, ctx, instanceB, collectionName, map[string]any{"body": "seen through listener"})
	if stringValue(t, record, "body") != "seen through listener" {
		t.Fatalf("instance B record = %#v", record)
	}
}

func bootstrapCRUDAndRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	waitReady(t, ctx, suite.baseURL)

	client := authenticatedClient(t, ctx, suite.baseURL)
	var currentSession map[string]any
	if err := client.request(ctx, http.MethodGet, "/api/admin/session", nil, http.StatusOK, &currentSession); err != nil {
		t.Fatal(err)
	}

	collectionName := uniqueCollection("posts")
	definition := map[string]any{
		"name": collectionName,
		"fields": []map[string]any{
			{"name": "title", "type": "text", "required": true, "unique": false, "indexed": true},
			{"name": "published", "type": "boolean", "required": false, "unique": false, "indexed": false},
		},
	}
	applyCollectionWithAuthChecks(t, ctx, client, definition)

	first := createRecord(t, ctx, client, collectionName, map[string]any{"title": "first", "published": false})
	second := createRecord(t, ctx, client, collectionName, map[string]any{"title": "second", "published": true})
	deleted := createRecord(t, ctx, client, collectionName, map[string]any{"title": "temporary", "published": false})

	firstID := stringValue(t, first, "id")
	secondID := stringValue(t, second, "id")
	deletedID := stringValue(t, deleted, "id")
	var fetched map[string]any
	if err := client.request(ctx, http.MethodGet, "/api/v1/collections/"+collectionName+"/records/"+firstID, nil, http.StatusOK, &fetched); err != nil {
		t.Fatal(err)
	}
	if fetched["title"] != "first" {
		t.Fatalf("fetched title = %#v", fetched["title"])
	}
	var updated map[string]any
	if err := client.request(ctx, http.MethodPatch, "/api/v1/collections/"+collectionName+"/records/"+firstID, map[string]any{"title": "first revised"}, http.StatusOK, &updated); err != nil {
		t.Fatal(err)
	}
	if updated["title"] != "first revised" {
		t.Fatalf("updated title = %#v", updated["title"])
	}
	if err := client.request(ctx, http.MethodDelete, "/api/v1/collections/"+collectionName+"/records/"+deletedID, nil, http.StatusNoContent, nil); err != nil {
		t.Fatal(err)
	}
	if err := client.request(ctx, http.MethodGet, "/api/v1/collections/"+collectionName+"/records/"+deletedID, nil, http.StatusNotFound, nil); err != nil {
		t.Fatal(err)
	}
	t.Run("all physical types", func(t *testing.T) {
		testAllPhysicalTypes(t, ctx, client)
	})

	ids := twoPageRecordIDs(t, ctx, client, collectionName)
	if len(ids) != 2 || !contains(ids, firstID) || !contains(ids, secondID) {
		t.Fatalf("paged record ids = %v", ids)
	}

	if output, err := suite.compose(ctx, "restart", "pgbase"); err != nil {
		t.Fatalf("restart pgbase without PostgreSQL: %v\n%s", err, output)
	}
	waitReady(t, ctx, suite.baseURL)

	restarted := newAPIClient(suite.baseURL)
	if err := restarted.login(ctx, suite.administrator, suite.password); err != nil {
		t.Fatal(err)
	}
	ids = twoPageRecordIDs(t, ctx, restarted, collectionName)
	if len(ids) != 2 || !contains(ids, firstID) || !contains(ids, secondID) {
		t.Fatalf("persisted record ids after restart = %v", ids)
	}
	for id, wantTitle := range map[string]string{firstID: "first revised", secondID: "second"} {
		var persisted map[string]any
		if err := restarted.request(ctx, http.MethodGet, "/api/v1/collections/"+collectionName+"/records/"+id, nil, http.StatusOK, &persisted); err != nil {
			t.Fatal(err)
		}
		if persisted["title"] != wantTitle {
			t.Fatalf("persisted title for %s = %#v, want %q", id, persisted["title"], wantTitle)
		}
	}
}

func configureSuite() (e2eSuite, error) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		return e2eSuite{}, fmt.Errorf("locate test source")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	httpPort, err := freePort()
	if err != nil {
		return e2eSuite{}, err
	}
	postgresPort, err := freePort()
	if err != nil {
		return e2eSuite{}, err
	}
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	project := strings.ToLower(fmt.Sprintf("pgbasee2e%d%s", os.Getpid(), suffix))
	setupToken, err := randomHex(32)
	if err != nil {
		return e2eSuite{}, err
	}
	rawSecret := make([]byte, 32)
	if _, err := rand.Read(rawSecret); err != nil {
		return e2eSuite{}, err
	}
	databasePass, err := randomHex(24)
	if err != nil {
		return e2eSuite{}, err
	}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", httpPort)
	image := project + ":test"
	version := "e2e-test"
	environment := append(os.Environ(),
		"PGBASE_HTTP_PORT="+strconv.Itoa(httpPort),
		"PGBASE_POSTGRES_PORT="+strconv.Itoa(postgresPort),
		"PGBASE_POSTGRES_PASSWORD="+databasePass,
		"PGBASE_SETUP_TOKEN="+setupToken,
		"PGBASE_TOKEN_SECRET="+base64.StdEncoding.EncodeToString(rawSecret),
		"PGBASE_PUBLIC_URL="+baseURL,
		"PGBASE_IMAGE="+image,
		"PGBASE_VERSION="+version,
	)
	return e2eSuite{
		repoRoot: repoRoot, composeFile: filepath.Join(repoRoot, "deployments", "docker-compose.yml"),
		project: project, image: image, baseURL: baseURL, httpPort: httpPort, postgresPort: postgresPort,
		setupToken: setupToken, tokenSecret: base64.StdEncoding.EncodeToString(rawSecret), databasePass: databasePass,
		administrator: "admin@example.test", password: "correct horse battery staple 2026!", version: version, environment: environment,
	}, nil
}

func (s e2eSuite) compose(ctx context.Context, arguments ...string) (string, error) {
	args := []string{"compose", "--project-name", s.project, "--file", s.composeFile}
	args = append(args, arguments...)
	command := exec.CommandContext(ctx, "docker", args...)
	command.Dir = s.repoRoot
	command.Env = s.environment
	output, err := command.CombinedOutput()
	return string(output), err
}

func (s e2eSuite) docker(ctx context.Context, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, "docker", arguments...)
	command.Dir = s.repoRoot
	command.Env = s.environment
	output, err := command.CombinedOutput()
	return string(output), err
}

func (s e2eSuite) cleanup() error {
	var cleanupErrors []error
	composeCtx, composeCancel := context.WithTimeout(context.Background(), 30*time.Second)
	output, err := s.compose(composeCtx, "down", "--volumes", "--remove-orphans")
	composeCancel()
	if err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("clean compose project: %w (output: %s)", err, strings.TrimSpace(output)))
	}
	imageCtx, imageCancel := context.WithTimeout(context.Background(), 15*time.Second)
	output, err = s.docker(imageCtx, "image", "rm", s.image)
	imageCancel()
	if err != nil && !strings.Contains(output, "No such image") {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("remove image: %w (output: %s)", err, strings.TrimSpace(output)))
	}
	return errors.Join(cleanupErrors...)
}

func (s e2eSuite) writeFailureLogs() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	output, err := s.compose(ctx, "logs", "--no-color", "pgbase", "postgres")
	if err != nil {
		fmt.Fprintf(os.Stderr, "capture e2e service logs: %v\n%s\n", err, output)
		return
	}
	fmt.Fprintf(os.Stderr, "e2e service logs:\n%s\n", output)
}

func (s e2eSuite) startInstance(t *testing.T, ctx context.Context, suffix string, shutdownTimeout time.Duration) (string, string) {
	t.Helper()
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	name := s.project + "-" + suffix
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	arguments := []string{
		"run", "--detach", "--name", name,
		"--network", s.project + "_default",
		"--publish", fmt.Sprintf("127.0.0.1:%d:8090", port),
		"--read-only", "--tmpfs", "/tmp:size=16m,mode=1777",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges:true",
		"--env", "PGBASE_DATABASE_URL=postgres://pgbase:" + s.databasePass + "@postgres:5432/pgbase?sslmode=disable",
		"--env", "PGBASE_HTTP_ADDRESS=:8090",
		"--env", "PGBASE_PUBLIC_URL=" + baseURL,
		"--env", "PGBASE_SETUP_TOKEN=" + s.setupToken,
		"--env", "PGBASE_TOKEN_SECRET=" + s.tokenSecret,
		"--env", "PGBASE_SHUTDOWN_TIMEOUT=" + shutdownTimeout.String(),
		s.image, "serve",
	}
	if output, err := s.docker(ctx, arguments...); err != nil {
		t.Fatalf("start %s: %v\n%s", suffix, err, output)
	}
	return baseURL, name
}

func (s e2eSuite) removeContainer(t *testing.T, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	output, err := s.docker(ctx, "rm", "--force", name)
	if err != nil && !strings.Contains(output, "No such container") {
		t.Errorf("remove container %s: %v\n%s", name, err, output)
	}
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func randomHex(bytesCount int) (string, error) {
	value := make([]byte, bytesCount)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func waitReady(t *testing.T, ctx context.Context, baseURL string) {
	t.Helper()
	client := newAPIClient(baseURL)
	deadline := time.Now().Add(90 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		lastErr = client.ready(requestCtx)
		cancel()
		if lastErr == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("wait for readiness: %v", lastErr)
}

func applyCollection(t *testing.T, ctx context.Context, client *apiClient, definition map[string]any) {
	t.Helper()
	apply := planCollection(t, ctx, client, definition)
	if err := client.request(ctx, http.MethodPost, "/api/admin/collections/apply", apply, http.StatusCreated, nil); err != nil {
		t.Fatal(err)
	}
}

func planCollection(t *testing.T, ctx context.Context, client *apiClient, definition map[string]any) map[string]any {
	t.Helper()
	var plan struct {
		BaseVersion int64  `json:"baseVersion"`
		Checksum    string `json:"checksum"`
	}
	if err := client.request(ctx, http.MethodPost, "/api/admin/collections/plan", definition, http.StatusOK, &plan); err != nil {
		t.Fatal(err)
	}
	apply := make(map[string]any, len(definition)+2)
	for key, value := range definition {
		apply[key] = value
	}
	apply["baseVersion"] = plan.BaseVersion
	apply["checksum"] = plan.Checksum
	return apply
}

func applyCollectionWithAuthChecks(t *testing.T, ctx context.Context, client *apiClient, definition map[string]any) {
	t.Helper()
	apply := planCollection(t, ctx, client, definition)
	collectionName, ok := definition["name"].(string)
	if !ok || collectionName == "" {
		t.Fatalf("collection name = %#v", definition["name"])
	}
	var before struct {
		Version     int64                      `json:"version"`
		Collections map[string]json.RawMessage `json:"collections"`
	}
	if err := client.request(ctx, http.MethodGet, "/api/admin/collections", nil, http.StatusOK, &before); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		requester  *apiClient
		wantStatus int
		wantCode   string
	}{
		{name: "no session", requester: newAPIClient(client.baseURL), wantStatus: http.StatusUnauthorized, wantCode: "authentication_required"},
		{name: "missing CSRF", requester: &apiClient{baseURL: client.baseURL, http: client.http}, wantStatus: http.StatusForbidden, wantCode: "csrf_rejected"},
		{name: "invalid CSRF", requester: &apiClient{baseURL: client.baseURL, http: client.http, csrf: "invalid-csrf-token"}, wantStatus: http.StatusForbidden, wantCode: "csrf_rejected"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var failure struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := test.requester.request(ctx, http.MethodPost, "/api/admin/collections/apply", apply, test.wantStatus, &failure); err != nil {
				t.Fatal(err)
			}
			if failure.Error.Code != test.wantCode {
				t.Fatalf("error code = %q, want %q", failure.Error.Code, test.wantCode)
			}
		})
	}

	var after struct {
		Version     int64                      `json:"version"`
		Collections map[string]json.RawMessage `json:"collections"`
	}
	if err := client.request(ctx, http.MethodGet, "/api/admin/collections", nil, http.StatusOK, &after); err != nil {
		t.Fatal(err)
	}
	if after.Version != before.Version {
		t.Fatalf("catalog version changed after rejected mutations: before=%d after=%d", before.Version, after.Version)
	}
	if _, exists := after.Collections[collectionName]; exists {
		t.Fatalf("collection %q exists after rejected mutations", collectionName)
	}
	if err := client.request(ctx, http.MethodPost, "/api/admin/collections/apply", apply, http.StatusCreated, nil); err != nil {
		t.Fatal(err)
	}
}

func authenticatedClient(t *testing.T, ctx context.Context, baseURL string) *apiClient {
	t.Helper()
	client := newAPIClient(baseURL)
	if err := client.login(ctx, suite.administrator, suite.password); err == nil {
		return client
	}
	if err := client.setup(ctx, suite.setupToken, suite.administrator, suite.password); err == nil {
		return client
	}
	client = newAPIClient(baseURL)
	if err := client.login(ctx, suite.administrator, suite.password); err != nil {
		t.Fatal(err)
	}
	return client
}

func createRecord(t *testing.T, ctx context.Context, client *apiClient, collection string, input map[string]any) map[string]any {
	t.Helper()
	var record map[string]any
	path := "/api/v1/collections/" + url.PathEscape(collection) + "/records"
	if err := client.request(ctx, http.MethodPost, path, input, http.StatusCreated, &record); err != nil {
		t.Fatal(err)
	}
	return record
}

func testAllPhysicalTypes(t *testing.T, ctx context.Context, client *apiClient) {
	t.Helper()
	collectionName := uniqueCollection("all_types")
	definition := map[string]any{
		"name": collectionName,
		"fields": []map[string]any{
			{"name": "bool_value", "type": "boolean", "required": false, "unique": false, "indexed": false, "default": false},
			{"name": "integer_value", "type": "integer", "required": false, "unique": false, "indexed": false, "default": "-7"},
			{"name": "bigint_value", "type": "bigint", "required": false, "unique": false, "indexed": false, "default": "9007199254740993"},
			{"name": "numeric_value", "type": "numeric", "required": false, "unique": false, "indexed": false, "default": "9007199254740993.00000000000000000001"},
			{"name": "text_value", "type": "text", "required": false, "unique": false, "indexed": false, "default": "default text"},
			{"name": "timestamp_value", "type": "timestamptz", "required": false, "unique": false, "indexed": false, "default": "2026-08-28T12:34:56Z"},
			{"name": "uuid_value", "type": "uuid", "required": false, "unique": false, "indexed": false, "default": "12345678-1234-4567-8901-123456789ABC"},
			{"name": "jsonb_value", "type": "jsonb", "required": false, "unique": false, "indexed": false, "default": []any{1, map[string]any{"default": true}}},
		},
	}
	applyCollection(t, ctx, client, definition)

	createdWant := map[string]string{
		"bool_value":      "true",
		"integer_value":   "-2147483648",
		"bigint_value":    `"9223372036854775807"`,
		"numeric_value":   `"12345678901234567890.12345678901234567890"`,
		"text_value":      `"created"`,
		"timestamp_value": `"2026-08-28T12:34:56.123456Z"`,
		"uuid_value":      `"12345678-1234-4567-8901-123456789abc"`,
		"jsonb_value":     `["created",1,true]`,
	}
	createdInput := map[string]any{
		"bool_value":      true,
		"integer_value":   -2147483648,
		"bigint_value":    "9223372036854775807",
		"numeric_value":   "12345678901234567890.12345678901234567890",
		"text_value":      "created",
		"timestamp_value": "2026-08-28T21:34:56.123456+09:00",
		"uuid_value":      "12345678-1234-4567-8901-123456789ABC",
		"jsonb_value":     []any{"created", 1, true},
	}
	created := createRawRecord(t, ctx, client, collectionName, createdInput)
	assertRawFields(t, "create", created, createdWant)
	createdID := rawStringValue(t, created, "id")

	var fetched map[string]json.RawMessage
	if err := client.request(ctx, http.MethodGet, "/api/v1/collections/"+collectionName+"/records/"+createdID, nil, http.StatusOK, &fetched); err != nil {
		t.Fatal(err)
	}
	assertRawFields(t, "get", fetched, createdWant)

	page := fetchRecordPage(t, ctx, client, collectionName, 50, "")
	if len(page.Items) != 1 || page.NextCursor != "" {
		t.Fatalf("all-types list page: items=%d cursor=%q", len(page.Items), page.NextCursor)
	}
	if rawStringValue(t, page.Items[0], "id") != createdID {
		t.Fatalf("all-types list id = %s, want %s", page.Items[0]["id"], createdID)
	}
	assertRawFields(t, "list", page.Items[0], createdWant)

	updatedWant := map[string]string{
		"bool_value":      "false",
		"integer_value":   "2147483647",
		"bigint_value":    `"-9223372036854775808"`,
		"numeric_value":   `"-99999999999999999999.00000000000000000001"`,
		"text_value":      `"updated"`,
		"timestamp_value": `"2026-08-28T00:00:00.000001Z"`,
		"uuid_value":      `"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"`,
		"jsonb_value":     `[3,{"updated":true}]`,
	}
	updatedInput := map[string]any{
		"bool_value":      false,
		"integer_value":   2147483647,
		"bigint_value":    "-9223372036854775808",
		"numeric_value":   "-99999999999999999999.00000000000000000001",
		"text_value":      "updated",
		"timestamp_value": "2026-08-28T00:00:00.000001Z",
		"uuid_value":      "AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE",
		"jsonb_value":     []any{3, map[string]any{"updated": true}},
	}
	var updated map[string]json.RawMessage
	if err := client.request(ctx, http.MethodPatch, "/api/v1/collections/"+collectionName+"/records/"+createdID, updatedInput, http.StatusOK, &updated); err != nil {
		t.Fatal(err)
	}
	assertRawFields(t, "patch", updated, updatedWant)

	nullInput := make(map[string]any, len(updatedWant))
	nullWant := make(map[string]string, len(updatedWant))
	for field := range updatedWant {
		nullInput[field] = nil
		nullWant[field] = "null"
	}
	var nulled map[string]json.RawMessage
	if err := client.request(ctx, http.MethodPatch, "/api/v1/collections/"+collectionName+"/records/"+createdID, nullInput, http.StatusOK, &nulled); err != nil {
		t.Fatal(err)
	}
	assertRawFields(t, "null patch", nulled, nullWant)

	defaultsWant := map[string]string{
		"bool_value":      "false",
		"integer_value":   "-7",
		"bigint_value":    `"9007199254740993"`,
		"numeric_value":   `"9007199254740993.00000000000000000001"`,
		"text_value":      `"default text"`,
		"timestamp_value": `"2026-08-28T12:34:56Z"`,
		"uuid_value":      `"12345678-1234-4567-8901-123456789abc"`,
		"jsonb_value":     `[1,{"default":true}]`,
	}
	defaulted := createRawRecord(t, ctx, client, collectionName, map[string]any{})
	assertRawFields(t, "defaults", defaulted, defaultsWant)
}

func createRawRecord(t *testing.T, ctx context.Context, client *apiClient, collection string, input map[string]any) map[string]json.RawMessage {
	t.Helper()
	var record map[string]json.RawMessage
	path := "/api/v1/collections/" + url.PathEscape(collection) + "/records"
	if err := client.request(ctx, http.MethodPost, path, input, http.StatusCreated, &record); err != nil {
		t.Fatal(err)
	}
	return record
}

func assertRawFields(t *testing.T, operation string, record map[string]json.RawMessage, want map[string]string) {
	t.Helper()
	for field, expected := range want {
		if actual := string(record[field]); actual != expected {
			t.Fatalf("%s %s = %s, want %s", operation, field, actual, expected)
		}
	}
}

func rawStringValue(t *testing.T, values map[string]json.RawMessage, key string) string {
	t.Helper()
	var value string
	if err := json.Unmarshal(values[key], &value); err != nil || value == "" {
		t.Fatalf("decode %s: value=%s error=%v", key, values[key], err)
	}
	return value
}

type recordPage struct {
	Items      []map[string]json.RawMessage `json:"items"`
	NextCursor string                       `json:"nextCursor"`
}

func fetchRecordPage(t *testing.T, ctx context.Context, client *apiClient, collection string, limit int, cursor string) recordPage {
	t.Helper()
	path := "/api/v1/collections/" + url.PathEscape(collection) + "/records"
	values := url.Values{"limit": []string{strconv.Itoa(limit)}}
	if cursor != "" {
		values.Set("cursor", cursor)
	}
	var page recordPage
	if err := client.request(ctx, http.MethodGet, queryPath(path, values), nil, http.StatusOK, &page); err != nil {
		t.Fatal(err)
	}
	return page
}

func twoPageRecordIDs(t *testing.T, ctx context.Context, client *apiClient, collection string) []string {
	t.Helper()
	first := fetchRecordPage(t, ctx, client, collection, 1, "")
	if len(first.Items) != 1 {
		t.Fatalf("first page item count = %d, want 1", len(first.Items))
	}
	if first.NextCursor == "" {
		t.Fatal("first page cursor is empty")
	}
	second := fetchRecordPage(t, ctx, client, collection, 1, first.NextCursor)
	if len(second.Items) != 1 {
		t.Fatalf("second page item count = %d, want 1", len(second.Items))
	}
	if second.NextCursor != "" {
		t.Fatalf("second page cursor = %q, want termination", second.NextCursor)
	}
	ids := make([]string, 0, 2)
	for pageNumber, item := range []map[string]json.RawMessage{first.Items[0], second.Items[0]} {
		var id string
		if err := json.Unmarshal(item["id"], &id); err != nil || id == "" {
			t.Fatalf("decode page %d record id: value=%s error=%v", pageNumber+1, item["id"], err)
		}
		ids = append(ids, id)
	}
	if ids[0] == ids[1] {
		t.Fatalf("page record ids are not distinct: %v", ids)
	}
	return ids
}

func stringValue(t *testing.T, values map[string]any, key string) string {
	t.Helper()
	value, ok := values[key].(string)
	if !ok || value == "" {
		t.Fatalf("%s = %#v", key, values[key])
	}
	return value
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
