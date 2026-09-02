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
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/tests/testsupport"
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
	metricsToken  string
	databasePass  string
	administrator string
	password      string
	version       string
	release       bool
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
	startupCtx, startupCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	startupArguments := []string{"up", "-d"}
	if suite.release {
		if output, pullErr := suite.compose(startupCtx, "pull", "pgbase"); pullErr != nil {
			startupCancel()
			fmt.Fprintf(os.Stderr, "pull digest-pinned e2e candidate: %v\n%s\n", pullErr, output)
			os.Exit(1)
		}
		startupArguments = append(startupArguments, "--no-build")
	} else {
		startupArguments = append(startupArguments, "--build")
	}
	output, err := suite.compose(startupCtx, startupArguments...)
	startupCancel()
	if err != nil {
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

func TestMetricsEndpointRequiresBearerAndUsesBoundedLabels(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	waitReady(t, ctx, suite.baseURL)

	sentinel := "metrics-secret-record-019551d0-e9c0-7000-8000-000000000999"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, suite.baseURL+"/health/live?probe="+url.QueryEscape(sentinel), nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("liveness status = %d", response.StatusCode)
	}

	for _, token := range []string{"", "wrong-" + suite.metricsToken} {
		status, _ := scrapeMetrics(t, ctx, token)
		if status != http.StatusUnauthorized {
			t.Fatalf("metrics with token %q status = %d, want 401", token, status)
		}
	}
	status, body := scrapeMetrics(t, ctx, suite.metricsToken)
	if status != http.StatusOK {
		t.Fatalf("authenticated metrics status = %d body=%s", status, body)
	}
	for _, metric := range []string{
		"pgbase_http_requests_total",
		"pgbase_database_pool_max_connections",
		"pgbase_application_readiness",
	} {
		if !strings.Contains(body, metric) {
			t.Fatalf("metrics omitted %q", metric)
		}
	}
	if strings.Contains(body, sentinel) {
		t.Fatalf("metrics leaked raw path/query sentinel %q", sentinel)
	}
	if !strings.Contains(body, `route="/health/live"`) {
		t.Fatalf("metrics omitted bounded liveness route template")
	}
}

func scrapeMetrics(t *testing.T, ctx context.Context, token string) (int, string) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, suite.baseURL+"/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(body)
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
	if err := testsupport.Poll(ctx, 10*time.Second, 100*time.Millisecond, 3*time.Second, func(attemptCtx context.Context) (bool, error) {
		err := client.request(attemptCtx, http.MethodGet, "/health/ready", nil, http.StatusServiceUnavailable, nil)
		return err == nil, err
	}); err != nil {
		t.Fatalf("readiness did not report PostgreSQL outage: %v", err)
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

	if err := testsupport.Poll(ctx, 15*time.Second, 100*time.Millisecond, 3*time.Second, func(attemptCtx context.Context) (bool, error) {
		var catalog struct {
			Collections map[string]json.RawMessage `json:"collections"`
		}
		if err := instanceB.request(attemptCtx, http.MethodGet, "/api/admin/collections", nil, http.StatusOK, &catalog); err != nil {
			return false, err
		}
		_, present := catalog.Collections[collectionName]
		return present, nil
	}); err != nil {
		t.Fatalf("instance B did not observe schema applied through A: %v", err)
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

func TestCollectionLifecycleAndRecordQueries(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	waitReady(t, ctx, suite.baseURL)
	client := authenticatedClient(t, ctx, suite.baseURL)

	collectionName := uniqueCollection("lifecycle")
	definition := map[string]any{
		"name": collectionName,
		"fields": []map[string]any{
			{"name": "title", "type": "text", "required": true, "unique": false, "indexed": false},
			{"name": "rank", "type": "integer", "required": false, "unique": false, "indexed": true},
		},
	}
	applyCollection(t, ctx, client, definition)

	seed := []struct {
		title string
		rank  string
	}{{"alpha", ""}, {"bravo", "2"}, {"charlie", "1"}}
	recordIDs := make([]string, 0, len(seed))
	for _, entry := range seed {
		record := map[string]any{"title": entry.title}
		if entry.rank != "" {
			rank, parseErr := strconv.Atoi(entry.rank)
			if parseErr != nil {
				t.Fatalf("seed rank %q: %v", entry.rank, parseErr)
			}
			record["rank"] = rank
		}
		created := createRecord(t, ctx, client, collectionName, record)
		recordIDs = append(recordIDs, stringValue(t, created, "id"))
	}

	// Update: add a required-with-default field and widen nothing. The change
	// must be visible to new list queries without a process restart.
	updatedDefinition := map[string]any{
		"name": collectionName,
		"fields": []map[string]any{
			{"name": "title", "type": "text", "required": true, "unique": false, "indexed": false},
			{"name": "rank", "type": "integer", "required": false, "unique": false, "indexed": true},
			{"name": "stage", "type": "text", "required": false, "unique": false, "indexed": false, "default": "draft"},
		},
	}
	updatePlan := planCollectionOperation(t, ctx, client, updatedDefinition, "update")
	if err := client.request(ctx, http.MethodPost, "/api/admin/collections/apply", updatePlan, http.StatusOK, nil); err != nil {
		t.Fatal(err)
	}
	var refreshed map[string]any
	if err := client.request(ctx, http.MethodGet, "/api/v1/collections/"+collectionName+"/records/"+recordIDs[0], nil, http.StatusOK, &refreshed); err != nil {
		t.Fatal(err)
	}
	if refreshed["stage"] != "draft" {
		t.Fatalf("refreshed record stage = %#v, want default %q", refreshed["stage"], "draft")
	}

	// Filter and sort: filtering plus an indexed descending sort must compose with cursors.
	path := "/api/v1/collections/" + url.PathEscape(collectionName) + "/records"
	values := url.Values{"limit": []string{"50"}, "filter": []string{"rank >= 1"}, "sort": []string{"-rank"}}
	var filtered recordPage
	if err := client.request(ctx, http.MethodGet, queryPath(path, values), nil, http.StatusOK, &filtered); err != nil {
		t.Fatal(err)
	}
	filteredTitles := recordFieldValues(t, filtered, "title")
	if len(filteredTitles) != 2 || filteredTitles[0] != "bravo" || filteredTitles[1] != "charlie" {
		t.Fatalf("filtered titles = %#v, want [bravo charlie]", filteredTitles)
	}

	// Cursor continuation must preserve the filter and sort and terminate.
	// rank >= 1 matches exactly two rows (bravo, charlie), so the second
	// single-row page must end the cursor chain.
	paged := url.Values{"limit": []string{"1"}, "filter": []string{"rank >= 1"}, "sort": []string{"-rank"}}
	var first, second recordPage
	if err := client.request(ctx, http.MethodGet, queryPath(path, paged), nil, http.StatusOK, &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("first filtered page = %v cursor %q", recordFieldValues(t, first, "title"), first.NextCursor)
	}
	if got := recordFieldValues(t, first, "title"); got[0] != "bravo" {
		t.Fatalf("first filtered page title = %#v, want [bravo]", got)
	}
	paged.Set("cursor", first.NextCursor)
	if err := client.request(ctx, http.MethodGet, queryPath(path, paged), nil, http.StatusOK, &second); err != nil {
		t.Fatal(err)
	}
	secondTitles := recordFieldValues(t, second, "title")
	if len(secondTitles) != 1 || secondTitles[0] != "charlie" || second.NextCursor != "" {
		t.Fatalf("second filtered page = %v cursor %q, want [charlie] with termination", secondTitles, second.NextCursor)
	}

	invalid := url.Values{"limit": []string{"50"}, "filter": []string{"ghost = 1"}}
	if err := client.request(ctx, http.MethodGet, queryPath(path, invalid), nil, http.StatusUnprocessableEntity, nil); err != nil {
		t.Fatalf("unknown filter field must be rejected: %v", err)
	}

	// Delete: two-phase delete drops the table and the catalog entry.
	deletePlan := planCollectionOperation(t, ctx, client, map[string]any{"name": collectionName}, "delete")
	if err := client.request(ctx, http.MethodPost, "/api/admin/collections/apply", deletePlan, http.StatusOK, nil); err != nil {
		t.Fatal(err)
	}
	if err := client.request(ctx, http.MethodGet, path, nil, http.StatusNotFound, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRecordMutationGuards(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	waitReady(t, ctx, suite.baseURL)
	client := authenticatedClient(t, ctx, suite.baseURL)

	collectionName := uniqueCollection("mutation_guards")
	applyCollection(t, ctx, client, map[string]any{
		"name": collectionName,
		"fields": []map[string]any{
			{"name": "title", "type": "text", "required": true, "unique": false, "indexed": false},
		},
	})

	path := "/api/v1/collections/" + url.PathEscape(collectionName) + "/records"
	createHeaders := make(http.Header)
	createHeaders.Set("Idempotency-Key", "e2e-mutation-create-key-20260831")
	input := map[string]any{"title": "first"}
	var created map[string]any
	firstHeaders, err := client.requestWithHeaders(ctx, http.MethodPost, path, input, createHeaders, http.StatusCreated, &created)
	if err != nil {
		t.Fatal(err)
	}
	createdID := stringValue(t, created, "id")
	createdETag := firstHeaders.Get("ETag")
	if createdETag == "" || firstHeaders.Get("Idempotency-Replayed") != "false" || firstHeaders.Get("PgBase-Compatibility") != "" {
		t.Fatalf("initial create headers = %v", firstHeaders)
	}
	if want := path + "/" + createdID; firstHeaders.Get("Location") != want {
		t.Fatalf("initial create Location = %q, want %q", firstHeaders.Get("Location"), want)
	}

	var replayed map[string]any
	replayHeaders, err := client.requestWithHeaders(ctx, http.MethodPost, path, input, createHeaders, http.StatusCreated, &replayed)
	if err != nil {
		t.Fatal(err)
	}
	if stringValue(t, replayed, "id") != createdID || replayHeaders.Get("Idempotency-Replayed") != "true" ||
		replayHeaders.Get("ETag") != createdETag || replayHeaders.Get("Location") != firstHeaders.Get("Location") {
		t.Fatalf("replay record/headers = %#v / %v, want original id/ETag/Location", replayed, replayHeaders)
	}

	var conflict struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if _, err := client.requestWithHeaders(ctx, http.MethodPost, path, map[string]any{"title": "different"}, createHeaders, http.StatusConflict, &conflict); err != nil {
		t.Fatal(err)
	}
	if conflict.Error.Code != "idempotency_conflict" {
		t.Fatalf("idempotency conflict code = %q", conflict.Error.Code)
	}

	recordPath := path + "/" + createdID
	var fetched map[string]any
	getHeaders, err := client.requestWithHeaders(ctx, http.MethodGet, recordPath, nil, nil, http.StatusOK, &fetched)
	if err != nil {
		t.Fatal(err)
	}
	if getHeaders.Get("ETag") != createdETag {
		t.Fatalf("GET ETag = %q, want %q", getHeaders.Get("ETag"), createdETag)
	}

	currentHeaders := make(http.Header)
	currentHeaders.Set("If-Match", createdETag)
	var updated map[string]any
	updateResponseHeaders, err := client.requestWithHeaders(ctx, http.MethodPatch, recordPath, map[string]any{"title": "updated"}, currentHeaders, http.StatusOK, &updated)
	if err != nil {
		t.Fatal(err)
	}
	updatedETag := updateResponseHeaders.Get("ETag")
	if updated["title"] != "updated" || updatedETag == "" || updatedETag == createdETag || updateResponseHeaders.Get("PgBase-Compatibility") != "" {
		t.Fatalf("updated record/headers = %#v / %v", updated, updateResponseHeaders)
	}

	var stale struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	staleHeaders, err := client.requestWithHeaders(ctx, http.MethodPatch, recordPath, map[string]any{"title": "must-not-win"}, currentHeaders, http.StatusPreconditionFailed, &stale)
	if err != nil {
		t.Fatal(err)
	}
	if stale.Error.Code != "precondition_failed" || staleHeaders.Get("ETag") != updatedETag {
		t.Fatalf("stale response = %#v / %v, want current ETag %q", stale, staleHeaders, updatedETag)
	}
	var afterStale map[string]any
	if _, err := client.requestWithHeaders(ctx, http.MethodGet, recordPath, nil, nil, http.StatusOK, &afterStale); err != nil {
		t.Fatal(err)
	}
	if afterStale["title"] != "updated" {
		t.Fatalf("stale update changed record = %#v", afterStale)
	}
}

func TestTwoInstancesConvergeConcurrentSameETagUpdates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	waitReady(t, ctx, suite.baseURL)
	instanceA := authenticatedClient(t, ctx, suite.baseURL)

	collectionName := uniqueCollection("concurrent_updates")
	applyCollection(t, ctx, instanceA, map[string]any{
		"name": collectionName,
		"fields": []map[string]any{
			{"name": "title", "type": "text", "required": true, "unique": false, "indexed": false},
		},
	})

	recordsPath := "/api/v1/collections/" + url.PathEscape(collectionName) + "/records"
	createHeaders := make(http.Header)
	createHeaders.Set("Idempotency-Key", "e2e-concurrent-update-create-20260831")
	var created map[string]any
	createdHeaders, err := instanceA.requestWithHeaders(ctx, http.MethodPost, recordsPath,
		map[string]any{"title": "initial"}, createHeaders, http.StatusCreated, &created)
	if err != nil {
		t.Fatal(err)
	}
	recordPath := recordsPath + "/" + stringValue(t, created, "id")
	initialETag := createdHeaders.Get("ETag")
	if initialETag == "" {
		t.Fatal("created record omitted ETag")
	}

	instanceBURL, instanceBName := suite.startInstance(t, ctx, "record-mutation-b", 12*time.Second)
	t.Cleanup(func() { suite.removeContainer(t, instanceBName) })
	waitReady(t, ctx, instanceBURL)
	instanceB := authenticatedClient(t, ctx, instanceBURL)

	type mutationOutcome struct {
		name          string
		intendedTitle string
		response      apiHTTPResponse
		err           error
	}
	start := make(chan struct{})
	outcomes := make(chan mutationOutcome, 2)
	for _, attempt := range []struct {
		name   string
		client *apiClient
		title  string
	}{
		{name: "instance A", client: instanceA, title: "written by A"},
		{name: "instance B", client: instanceB, title: "written by B"},
	} {
		attempt := attempt
		go func() {
			<-start
			headers := make(http.Header)
			headers.Set("If-Match", initialETag)
			response, err := attempt.client.exchangeWithHeaders(ctx, http.MethodPatch, recordPath,
				map[string]any{"title": attempt.title}, headers)
			outcomes <- mutationOutcome{
				name: attempt.name, intendedTitle: attempt.title, response: response, err: err,
			}
		}()
	}
	close(start)

	var winner, loser *mutationOutcome
	for range 2 {
		outcome := <-outcomes
		if outcome.err != nil {
			t.Fatalf("%s concurrent update: %v", outcome.name, outcome.err)
		}
		switch outcome.response.Status {
		case http.StatusOK:
			if winner != nil {
				t.Fatalf("both concurrent updates succeeded: first=%s second=%s", winner.name, outcome.name)
			}
			winner = &outcome
		case http.StatusPreconditionFailed:
			if loser != nil {
				t.Fatalf("both concurrent updates failed preconditions: first=%s second=%s", loser.name, outcome.name)
			}
			loser = &outcome
		default:
			t.Fatalf("%s concurrent update status=%d headers=%v body=%s", outcome.name,
				outcome.response.Status, outcome.response.Header, outcome.response.Body)
		}
	}
	if winner == nil || loser == nil {
		t.Fatalf("concurrent update outcomes winner=%v loser=%v", winner, loser)
	}

	var committed map[string]any
	if err := json.Unmarshal(winner.response.Body, &committed); err != nil {
		t.Fatalf("decode winning update: %v (body=%s)", err, winner.response.Body)
	}
	committedTitle := stringValue(t, committed, "title")
	if committedTitle != winner.intendedTitle {
		t.Fatalf("winning update through %s returned title=%q, want %q", winner.name, committedTitle, winner.intendedTitle)
	}
	committedETag := winner.response.Header.Get("ETag")
	if committedETag == "" || committedETag == initialETag {
		t.Fatalf("winning update ETag=%q, initial=%q", committedETag, initialETag)
	}
	var rejected struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(loser.response.Body, &rejected); err != nil {
		t.Fatalf("decode losing update: %v (body=%s)", err, loser.response.Body)
	}
	if rejected.Error.Code != "precondition_failed" || loser.response.Header.Get("ETag") != committedETag {
		t.Fatalf("losing update code=%q ETag=%q, want precondition_failed/current %q", rejected.Error.Code,
			loser.response.Header.Get("ETag"), committedETag)
	}

	for _, replica := range []struct {
		name   string
		client *apiClient
	}{{name: "instance A", client: instanceA}, {name: "instance B", client: instanceB}} {
		var fetched map[string]any
		headers, err := replica.client.requestWithHeaders(ctx, http.MethodGet, recordPath, nil, nil, http.StatusOK, &fetched)
		if err != nil {
			t.Fatalf("read committed record through %s: %v", replica.name, err)
		}
		if stringValue(t, fetched, "title") != committedTitle || headers.Get("ETag") != committedETag {
			t.Fatalf("%s observed record=%#v ETag=%q, want title=%q ETag=%q", replica.name, fetched,
				headers.Get("ETag"), committedTitle, committedETag)
		}
	}
}

func planCollectionOperation(t *testing.T, ctx context.Context, client *apiClient, definition map[string]any, operation string) map[string]any {
	t.Helper()
	planBody := make(map[string]any, len(definition)+1)
	for key, value := range definition {
		planBody[key] = value
	}
	planBody["operation"] = operation
	var plan struct {
		BaseVersion int64  `json:"baseVersion"`
		Checksum    string `json:"checksum"`
	}
	if err := client.request(ctx, http.MethodPost, "/api/admin/collections/plan", planBody, http.StatusOK, &plan); err != nil {
		t.Fatal(err)
	}
	apply := make(map[string]any, len(planBody)+2)
	for key, value := range planBody {
		apply[key] = value
	}
	apply["baseVersion"] = plan.BaseVersion
	apply["checksum"] = plan.Checksum
	return apply
}

func recordFieldValues(t *testing.T, page recordPage, key string) []string {
	t.Helper()
	values := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		var value string
		if err := json.Unmarshal(item[key], &value); err != nil {
			t.Fatalf("decode %s: %v", key, err)
		}
		values = append(values, value)
	}
	return values
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
	metricsToken, err := randomHex(32)
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
	release := false
	candidateImage := os.Getenv("PGBASE_E2E_CANDIDATE_IMAGE")
	expectedVersion := os.Getenv("PGBASE_E2E_EXPECTED_VERSION")
	if (candidateImage == "") != (expectedVersion == "") {
		return e2eSuite{}, fmt.Errorf("PGBASE_E2E_CANDIDATE_IMAGE and PGBASE_E2E_EXPECTED_VERSION must be set together")
	}
	if candidateImage != "" {
		candidatePattern := regexp.MustCompile(`^[^@\s]+@sha256:[0-9a-f]{64}$`)
		if !candidatePattern.MatchString(candidateImage) {
			return e2eSuite{}, fmt.Errorf("PGBASE_E2E_CANDIDATE_IMAGE must be an image@sha256 digest reference without a mutable tag")
		}
		imageName := candidateImage[:strings.LastIndex(candidateImage, "@")]
		if strings.Contains(imageName[strings.LastIndex(imageName, "/")+1:], ":") {
			return e2eSuite{}, fmt.Errorf("PGBASE_E2E_CANDIDATE_IMAGE must be an image@sha256 digest reference without a mutable tag")
		}
		versionPattern := regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(alpha|beta|rc)\.(0|[1-9][0-9]*))?$`)
		if !versionPattern.MatchString(expectedVersion) {
			return e2eSuite{}, fmt.Errorf("PGBASE_E2E_EXPECTED_VERSION is not a supported canonical release version")
		}
		image = candidateImage
		version = expectedVersion
		release = true
	}
	environment := append(os.Environ(),
		"PGBASE_HTTP_PORT="+strconv.Itoa(httpPort),
		"PGBASE_POSTGRES_PORT="+strconv.Itoa(postgresPort),
		"PGBASE_POSTGRES_PASSWORD="+databasePass,
		"PGBASE_SETUP_TOKEN="+setupToken,
		"PGBASE_TOKEN_SECRET="+base64.StdEncoding.EncodeToString(rawSecret),
		"PGBASE_METRICS_TOKEN="+metricsToken,
		"PGBASE_PUBLIC_URL="+baseURL,
		"PGBASE_IMAGE="+image,
		"PGBASE_VERSION="+version,
		// The Compose drain test must observe a full graceful shutdown inside
		// the suite budget, so the project keeps the shipped five-second grace
		// margin at a shorter absolute timeout.
		"PGBASE_SHUTDOWN_TIMEOUT="+composeShutdownTimeout.String(),
		"PGBASE_STOP_GRACE_PERIOD="+composeStopGracePeriod.String(),
	)
	return e2eSuite{
		repoRoot: repoRoot, composeFile: filepath.Join(repoRoot, "deployments", "docker-compose.yml"),
		project: project, image: image, baseURL: baseURL, httpPort: httpPort, postgresPort: postgresPort,
		setupToken: setupToken, tokenSecret: base64.StdEncoding.EncodeToString(rawSecret), metricsToken: metricsToken, databasePass: databasePass,
		administrator: "admin@example.test", password: "correct horse battery staple 2026!", version: version, release: release, environment: environment,
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
	if !s.release {
		imageCtx, imageCancel := context.WithTimeout(context.Background(), 15*time.Second)
		output, err = s.docker(imageCtx, "image", "rm", s.image)
		imageCancel()
		if err != nil && !strings.Contains(output, "No such image") {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove image: %w (output: %s)", err, strings.TrimSpace(output)))
		}
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
		"--env", "PGBASE_METRICS_TOKEN=" + s.metricsToken,
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
	if err := testsupport.Poll(ctx, 90*time.Second, 250*time.Millisecond, 2*time.Second, func(attemptCtx context.Context) (bool, error) {
		err := client.ready(attemptCtx)
		return err == nil, err
	}); err != nil {
		t.Fatalf("wait for readiness: %v", err)
	}
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
	planBody := make(map[string]any, len(definition)+1)
	for key, value := range definition {
		planBody[key] = value
	}
	planBody["operation"] = "create"
	var plan struct {
		BaseVersion int64  `json:"baseVersion"`
		Checksum    string `json:"checksum"`
	}
	if err := client.request(ctx, http.MethodPost, "/api/admin/collections/plan", planBody, http.StatusOK, &plan); err != nil {
		t.Fatal(err)
	}
	apply := make(map[string]any, len(definition)+3)
	for key, value := range definition {
		apply[key] = value
	}
	apply["operation"] = "create"
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
