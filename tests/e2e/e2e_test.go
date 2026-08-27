//go:build e2e

package e2e

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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
	environment   []string
}

var suite e2eSuite

func TestMain(m *testing.M) {
	configured, err := configureSuite()
	if err != nil {
		fmt.Fprintln(os.Stderr, "configure e2e suite:", err)
		os.Exit(1)
	}
	suite = configured
	if output, err := suite.compose(context.Background(), "up", "-d", "--build"); err != nil {
		fmt.Fprintf(os.Stderr, "start e2e compose project: %v\n%s\n", err, output)
		suite.cleanup()
		os.Exit(1)
	}
	code := 1
	defer func() {
		suite.cleanup()
		os.Exit(code)
	}()
	code = m.Run()
}

func TestReadinessTracksDatabaseOutageAndRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	waitReady(t, ctx, suite.baseURL)
	if output, err := suite.compose(ctx, "pause", "postgres"); err != nil {
		t.Fatalf("pause PostgreSQL: %v\n%s", err, output)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_, _ = suite.compose(cleanupCtx, "unpause", "postgres")
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
	waitReady(t, ctx, suite.baseURL)
}

func TestTwoInstancesSynchronizeSchema(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	waitReady(t, ctx, suite.baseURL)
	instanceA := authenticatedClient(t, ctx, suite.baseURL)

	instanceBURL, instanceBName := suite.startInstance(t, ctx, "schema-b", 12*time.Second)
	t.Cleanup(func() { suite.removeContainer(instanceBName) })
	waitReady(t, ctx, instanceBURL)
	instanceB := authenticatedClient(t, ctx, instanceBURL)

	definition := map[string]any{
		"name": "shared_notes",
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
			if _, ok := catalog.Collections["shared_notes"]; ok {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("instance B did not observe schema applied through A: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	record := createRecord(t, ctx, instanceB, "shared_notes", map[string]any{"body": "seen through listener"})
	if stringValue(t, record, "body") != "seen through listener" {
		t.Fatalf("instance B record = %#v", record)
	}
}

func bootstrapCRUDAndRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	waitReady(t, ctx, suite.baseURL)

	client := newAPIClient(suite.baseURL)
	if err := client.setup(ctx, suite.setupToken, suite.administrator, suite.password); err != nil {
		t.Fatal(err)
	}
	var currentSession map[string]any
	if err := client.request(ctx, http.MethodGet, "/api/admin/session", nil, http.StatusOK, &currentSession); err != nil {
		t.Fatal(err)
	}

	definition := map[string]any{
		"name": "posts",
		"fields": []map[string]any{
			{"name": "title", "type": "text", "required": true, "unique": false, "indexed": true},
			{"name": "published", "type": "boolean", "required": false, "unique": false, "indexed": false},
		},
	}
	applyCollection(t, ctx, client, definition)

	first := createRecord(t, ctx, client, "posts", map[string]any{"title": "first", "published": false})
	second := createRecord(t, ctx, client, "posts", map[string]any{"title": "second", "published": true})
	deleted := createRecord(t, ctx, client, "posts", map[string]any{"title": "temporary", "published": false})

	firstID := stringValue(t, first, "id")
	secondID := stringValue(t, second, "id")
	deletedID := stringValue(t, deleted, "id")
	var fetched map[string]any
	if err := client.request(ctx, http.MethodGet, "/api/v1/collections/posts/records/"+firstID, nil, http.StatusOK, &fetched); err != nil {
		t.Fatal(err)
	}
	if fetched["title"] != "first" {
		t.Fatalf("fetched title = %#v", fetched["title"])
	}
	var updated map[string]any
	if err := client.request(ctx, http.MethodPatch, "/api/v1/collections/posts/records/"+firstID, map[string]any{"title": "first revised"}, http.StatusOK, &updated); err != nil {
		t.Fatal(err)
	}
	if updated["title"] != "first revised" {
		t.Fatalf("updated title = %#v", updated["title"])
	}
	if err := client.request(ctx, http.MethodDelete, "/api/v1/collections/posts/records/"+deletedID, nil, http.StatusNoContent, nil); err != nil {
		t.Fatal(err)
	}
	if err := client.request(ctx, http.MethodGet, "/api/v1/collections/posts/records/"+deletedID, nil, http.StatusNotFound, nil); err != nil {
		t.Fatal(err)
	}

	ids := pageRecordIDs(t, ctx, client, "posts", 1)
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
	ids = pageRecordIDs(t, ctx, restarted, "posts", 1)
	if len(ids) != 2 || !contains(ids, firstID) || !contains(ids, secondID) {
		t.Fatalf("persisted record ids after restart = %v", ids)
	}
	for id, wantTitle := range map[string]string{firstID: "first revised", secondID: "second"} {
		var persisted map[string]any
		if err := restarted.request(ctx, http.MethodGet, "/api/v1/collections/posts/records/"+id, nil, http.StatusOK, &persisted); err != nil {
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
	environment := append(os.Environ(),
		"PGBASE_HTTP_PORT="+strconv.Itoa(httpPort),
		"PGBASE_POSTGRES_PORT="+strconv.Itoa(postgresPort),
		"PGBASE_POSTGRES_PASSWORD="+databasePass,
		"PGBASE_SETUP_TOKEN="+setupToken,
		"PGBASE_TOKEN_SECRET="+base64.StdEncoding.EncodeToString(rawSecret),
		"PGBASE_PUBLIC_URL="+baseURL,
		"PGBASE_IMAGE="+image,
	)
	return e2eSuite{
		repoRoot: repoRoot, composeFile: filepath.Join(repoRoot, "deployments", "docker-compose.yml"),
		project: project, image: image, baseURL: baseURL, httpPort: httpPort, postgresPort: postgresPort,
		setupToken: setupToken, tokenSecret: base64.StdEncoding.EncodeToString(rawSecret), databasePass: databasePass,
		administrator: "admin@example.test", password: "correct horse battery staple 2026!", environment: environment,
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

func (s e2eSuite) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if output, err := s.compose(ctx, "down", "--volumes", "--remove-orphans"); err != nil {
		fmt.Fprintf(os.Stderr, "clean e2e compose project: %v\n%s\n", err, output)
	}
	if output, err := s.docker(ctx, "image", "rm", s.image); err != nil && !strings.Contains(output, "No such image") {
		fmt.Fprintf(os.Stderr, "remove e2e image: %v\n%s\n", err, output)
	}
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

func (s e2eSuite) removeContainer(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _ = s.docker(ctx, "rm", "--force", name)
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

func pageRecordIDs(t *testing.T, ctx context.Context, client *apiClient, collection string, limit int) []string {
	t.Helper()
	path := "/api/v1/collections/" + url.PathEscape(collection) + "/records"
	var ids []string
	cursor := ""
	for {
		values := url.Values{"limit": []string{strconv.Itoa(limit)}}
		if cursor != "" {
			values.Set("cursor", cursor)
		}
		var page struct {
			Items      []map[string]json.RawMessage `json:"items"`
			NextCursor string                       `json:"nextCursor"`
		}
		if err := client.request(ctx, http.MethodGet, queryPath(path, values), nil, http.StatusOK, &page); err != nil {
			t.Fatal(err)
		}
		for _, record := range page.Items {
			var id string
			if err := json.Unmarshal(record["id"], &id); err != nil {
				t.Fatalf("decode record id: %v", err)
			}
			ids = append(ids, id)
		}
		if page.NextCursor == "" {
			return ids
		}
		cursor = page.NextCursor
	}
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
