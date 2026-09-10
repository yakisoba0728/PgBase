//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/tests/testsupport"
)

type relationE2ERecord struct {
	data map[string]any
	etag string
}

type relationE2EPage struct {
	Items      []map[string]any `json:"items"`
	NextCursor string           `json:"nextCursor"`
}

func TestRelationCRUDExpandPersistsAcrossRestartAndReplicas(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	waitReady(t, ctx, suite.baseURL)
	instanceA := authenticatedClient(t, ctx, suite.baseURL)

	instanceBURL, instanceBName := suite.startInstance(t, ctx, "relation-read-b", 12*time.Second)
	t.Cleanup(func() { suite.removeContainer(t, instanceBName) })
	waitReady(t, ctx, instanceBURL)
	instanceB := authenticatedClient(t, ctx, instanceBURL)

	people := uniqueCollection("relation_people")
	articles := uniqueCollection("relation_articles")
	applyCollection(t, ctx, instanceA, map[string]any{
		"name": people,
		"fields": []map[string]any{
			{"name": "name", "type": "text", "required": true, "unique": false, "indexed": true},
			relationE2EField("manager", people, "single", "set_null", 1),
		},
	})
	applyCollection(t, ctx, instanceA, map[string]any{
		"name": articles,
		"fields": []map[string]any{
			{"name": "title", "type": "text", "required": true, "unique": false, "indexed": false},
			relationE2EField("owner", people, "single", "restrict", 1),
			relationE2EField("reviewers", people, "multiple", "restrict", 4),
		},
	})
	waitForE2ECollections(t, ctx, instanceB, people, articles)

	ada := createRelationE2ERecord(t, ctx, instanceA, people, "ada", map[string]any{"name": "Ada"})
	bob := createRelationE2ERecord(t, ctx, instanceA, people, "bob", map[string]any{"name": "Bob", "manager": ada.id(t)})
	grace := createRelationE2ERecord(t, ctx, instanceA, people, "grace", map[string]any{"name": "Grace"})
	article := createRelationE2ERecord(t, ctx, instanceA, articles, "article", map[string]any{
		"title": "compilers", "owner": ada.id(t), "reviewers": []string{bob.id(t), ada.id(t)},
	})
	secondArticle := createRelationE2ERecord(t, ctx, instanceA, articles, "second-article", map[string]any{
		"title": "databases", "owner": ada.id(t), "reviewers": []string{bob.id(t)},
	})

	raw := getRelationE2ERecord(t, ctx, instanceB, articles, article.id(t), "")
	if raw.data["owner"] != ada.id(t) {
		t.Fatalf("raw owner = %#v, want %q", raw.data["owner"], ada.id(t))
	}
	assertRelationE2EIDs(t, raw.data, "reviewers", []string{bob.id(t), ada.id(t)})

	expanded := getRelationE2ERecord(t, ctx, instanceB, articles, article.id(t), "owner,reviewers")
	if expanded.etag != raw.etag {
		t.Fatalf("expanded ETag = %q, raw ETag = %q", expanded.etag, raw.etag)
	}
	assertRelationE2EExpansion(t, expanded.data, "owner", []string{ada.id(t)})
	assertRelationE2EExpansion(t, expanded.data, "reviewers", []string{bob.id(t), ada.id(t)})

	updated := updateRelationE2ERecord(t, ctx, instanceA, articles, article.id(t), raw.etag, map[string]any{
		"owner": bob.id(t), "reviewers": []string{ada.id(t), grace.id(t)},
	})
	if updated.etag == raw.etag || updated.data["owner"] != bob.id(t) {
		t.Fatalf("updated relation record = %#v ETag=%q, previous=%q", updated.data, updated.etag, raw.etag)
	}
	assertRelationE2EIDs(t, updated.data, "reviewers", []string{ada.id(t), grace.id(t)})

	combined := listRelationE2ERecords(t, ctx, instanceB, articles, url.Values{
		"limit":  {"50"},
		"filter": {"reviewers = '" + ada.id(t) + "'"},
		"sort":   {"owner.name"},
		"expand": {"owner.manager"},
	})
	if len(combined.Items) != 1 || stringValue(t, combined.Items[0], "id") != article.id(t) {
		t.Fatalf("combined relation list = %#v, want article %q", combined.Items, article.id(t))
	}
	assertRelationE2ENestedExpansion(t, combined.Items[0], "owner", "manager", bob.id(t), ada.id(t))

	firstPage := listRelationE2ERecords(t, ctx, instanceB, articles, url.Values{
		"limit": {"1"}, "sort": {"owner.name"}, "expand": {"owner.manager"},
	})
	if len(firstPage.Items) != 1 || stringValue(t, firstPage.Items[0], "id") != secondArticle.id(t) || firstPage.NextCursor == "" {
		t.Fatalf("first relation-sort page = %#v cursor=%q", firstPage.Items, firstPage.NextCursor)
	}
	secondPage := listRelationE2ERecords(t, ctx, instanceB, articles, url.Values{
		"limit": {"1"}, "sort": {"owner.name"}, "expand": {"owner.manager"}, "cursor": {firstPage.NextCursor},
	})
	if len(secondPage.Items) != 1 || stringValue(t, secondPage.Items[0], "id") != article.id(t) || secondPage.NextCursor != "" {
		t.Fatalf("second relation-sort page = %#v cursor=%q", secondPage.Items, secondPage.NextCursor)
	}

	reversePath := articles + "_via_owner"
	reverse := getRelationE2ERecord(t, ctx, instanceB, people, bob.id(t), reversePath)
	assertRelationE2EExpansion(t, reverse.data, reversePath, []string{article.id(t)})

	if output, err := suite.compose(ctx, "restart", "pgbase"); err != nil {
		t.Fatalf("restart pgbase with relation data: %v\n%s", err, output)
	}
	waitReady(t, ctx, suite.baseURL)
	restarted := authenticatedClient(t, ctx, suite.baseURL)
	persisted := getRelationE2ERecord(t, ctx, restarted, articles, article.id(t), "owner,reviewers")
	if persisted.etag != updated.etag || persisted.data["owner"] != bob.id(t) {
		t.Fatalf("persisted relation record = %#v ETag=%q, want ETag=%q", persisted.data, persisted.etag, updated.etag)
	}
	assertRelationE2EIDs(t, persisted.data, "reviewers", []string{ada.id(t), grace.id(t)})
	assertRelationE2EExpansion(t, persisted.data, "owner", []string{bob.id(t)})
	assertRelationE2EExpansion(t, persisted.data, "reviewers", []string{ada.id(t), grace.id(t)})

	deleteRelationE2ERecord(t, ctx, restarted, articles, article.id(t), persisted.etag, http.StatusNoContent, nil)
	getRelationE2ERecordStatus(t, ctx, restarted, articles, article.id(t), "", http.StatusNotFound)
}

func TestRelationDeleteActionsUseTheRecordMutationContract(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	waitReady(t, ctx, suite.baseURL)
	client := authenticatedClient(t, ctx, suite.baseURL)

	targets := uniqueCollection("relation_delete_targets")
	restricted := uniqueCollection("relation_restricted")
	cascaded := uniqueCollection("relation_cascaded")
	nulled := uniqueCollection("relation_nulled")
	applyCollection(t, ctx, client, map[string]any{
		"name": targets,
		"fields": []map[string]any{
			{"name": "name", "type": "text", "required": true, "unique": false, "indexed": false},
		},
	})
	for _, definition := range []map[string]any{
		{
			"name": restricted,
			"fields": []map[string]any{
				{"name": "name", "type": "text", "required": true, "unique": false, "indexed": false},
				relationE2EField("target", targets, "single", "restrict", 1),
			},
		},
		{
			"name": cascaded,
			"fields": []map[string]any{
				{"name": "name", "type": "text", "required": true, "unique": false, "indexed": false},
				relationE2EField("target", targets, "single", "cascade", 1),
			},
		},
		{
			"name": nulled,
			"fields": []map[string]any{
				{"name": "name", "type": "text", "required": true, "unique": false, "indexed": false},
				relationE2EField("targets", targets, "multiple", "set_null", 4),
			},
		},
	} {
		applyCollection(t, ctx, client, definition)
	}

	restrictTarget := createRelationE2ERecord(t, ctx, client, targets, "restrict-target", map[string]any{"name": "restricted"})
	restrictSource := createRelationE2ERecord(t, ctx, client, restricted, "restrict-source", map[string]any{
		"name": "source", "target": restrictTarget.id(t),
	})
	var restrictedFailure struct {
		Error struct {
			Code    string              `json:"code"`
			Details map[string][]string `json:"details"`
		} `json:"error"`
	}
	deleteRelationE2ERecord(t, ctx, client, targets, restrictTarget.id(t), restrictTarget.etag,
		http.StatusUnprocessableEntity, &restrictedFailure)
	if restrictedFailure.Error.Code != "validation_failed" ||
		!slices.Contains(restrictedFailure.Error.Details["id"], "relation_delete_restricted") {
		t.Fatalf("restrict response = %#v", restrictedFailure)
	}
	if got := getRelationE2ERecord(t, ctx, client, targets, restrictTarget.id(t), ""); got.etag != restrictTarget.etag {
		t.Fatalf("failed restrict delete changed target ETag: got %q want %q", got.etag, restrictTarget.etag)
	}
	deleteRelationE2ERecord(t, ctx, client, restricted, restrictSource.id(t), restrictSource.etag, http.StatusNoContent, nil)
	deleteRelationE2ERecord(t, ctx, client, targets, restrictTarget.id(t), restrictTarget.etag, http.StatusNoContent, nil)

	cascadeTarget := createRelationE2ERecord(t, ctx, client, targets, "cascade-target", map[string]any{"name": "cascade"})
	cascadeSource := createRelationE2ERecord(t, ctx, client, cascaded, "cascade-source", map[string]any{
		"name": "source", "target": cascadeTarget.id(t),
	})
	deleteRelationE2ERecord(t, ctx, client, targets, cascadeTarget.id(t), cascadeTarget.etag, http.StatusNoContent, nil)
	getRelationE2ERecordStatus(t, ctx, client, targets, cascadeTarget.id(t), "", http.StatusNotFound)
	getRelationE2ERecordStatus(t, ctx, client, cascaded, cascadeSource.id(t), "", http.StatusNotFound)

	setNullTarget := createRelationE2ERecord(t, ctx, client, targets, "set-null-target", map[string]any{"name": "set null"})
	keeper := createRelationE2ERecord(t, ctx, client, targets, "set-null-keeper", map[string]any{"name": "keeper"})
	setNullSource := createRelationE2ERecord(t, ctx, client, nulled, "set-null-source", map[string]any{
		"name": "source", "targets": []string{setNullTarget.id(t), keeper.id(t)},
	})
	deleteRelationE2ERecord(t, ctx, client, targets, setNullTarget.id(t), setNullTarget.etag, http.StatusNoContent, nil)
	afterSetNull := getRelationE2ERecord(t, ctx, client, nulled, setNullSource.id(t), "")
	if afterSetNull.etag == setNullSource.etag {
		t.Fatalf("set-null did not change survivor ETag %q", afterSetNull.etag)
	}
	assertRelationE2EIDs(t, afterSetNull.data, "targets", []string{keeper.id(t)})
}

func relationE2EField(name, target, cardinality, onDelete string, maximum int) map[string]any {
	relation := map[string]any{
		"targetCollection": target,
		"cardinality":      cardinality,
		"onDelete":         onDelete,
	}
	if cardinality == "multiple" {
		relation["maxSelect"] = maximum
	}
	return map[string]any{
		"name": name, "type": "relation", "required": false, "unique": false, "indexed": true,
		"relation": relation,
	}
}

func createRelationE2ERecord(
	t *testing.T,
	ctx context.Context,
	client *apiClient,
	collection, keySuffix string,
	input map[string]any,
) relationE2ERecord {
	t.Helper()
	path := relationE2ERecordPath(collection, "")
	headers := make(http.Header)
	headers.Set("Idempotency-Key", "e2e-relation-"+collection+"-"+keySuffix)
	var record map[string]any
	responseHeaders, err := client.requestWithHeaders(ctx, http.MethodPost, path, input, headers, http.StatusCreated, &record)
	if err != nil {
		t.Fatal(err)
	}
	etag := responseHeaders.Get("ETag")
	if etag == "" || responseHeaders.Get("Idempotency-Replayed") != "false" || responseHeaders.Get("PgBase-Compatibility") != "" {
		t.Fatalf("strict relation create headers = %v", responseHeaders)
	}
	return relationE2ERecord{data: record, etag: etag}
}

func updateRelationE2ERecord(
	t *testing.T,
	ctx context.Context,
	client *apiClient,
	collection, id, etag string,
	input map[string]any,
) relationE2ERecord {
	t.Helper()
	headers := make(http.Header)
	headers.Set("If-Match", etag)
	var record map[string]any
	responseHeaders, err := client.requestWithHeaders(ctx, http.MethodPatch,
		relationE2ERecordPath(collection, id), input, headers, http.StatusOK, &record)
	if err != nil {
		t.Fatal(err)
	}
	updatedETag := responseHeaders.Get("ETag")
	if updatedETag == "" || responseHeaders.Get("PgBase-Compatibility") != "" {
		t.Fatalf("strict relation update headers = %v", responseHeaders)
	}
	return relationE2ERecord{data: record, etag: updatedETag}
}

func getRelationE2ERecord(
	t *testing.T,
	ctx context.Context,
	client *apiClient,
	collection, id, expand string,
) relationE2ERecord {
	t.Helper()
	path := relationE2ERecordPath(collection, id)
	if expand != "" {
		path = queryPath(path, url.Values{"expand": []string{expand}})
	}
	var record map[string]any
	headers, err := client.requestWithHeaders(ctx, http.MethodGet, path, nil, nil, http.StatusOK, &record)
	if err != nil {
		t.Fatal(err)
	}
	etag := headers.Get("ETag")
	if etag == "" {
		t.Fatalf("relation GET omitted ETag: headers=%v", headers)
	}
	return relationE2ERecord{data: record, etag: etag}
}

func listRelationE2ERecords(
	t *testing.T,
	ctx context.Context,
	client *apiClient,
	collection string,
	values url.Values,
) relationE2EPage {
	t.Helper()
	path := queryPath(relationE2ERecordPath(collection, ""), values)
	var page relationE2EPage
	if err := client.request(ctx, http.MethodGet, path, nil, http.StatusOK, &page); err != nil {
		t.Fatal(err)
	}
	return page
}

func getRelationE2ERecordStatus(
	t *testing.T,
	ctx context.Context,
	client *apiClient,
	collection, id, expand string,
	wantStatus int,
) {
	t.Helper()
	path := relationE2ERecordPath(collection, id)
	if expand != "" {
		path = queryPath(path, url.Values{"expand": []string{expand}})
	}
	if err := client.request(ctx, http.MethodGet, path, nil, wantStatus, nil); err != nil {
		t.Fatal(err)
	}
}

func deleteRelationE2ERecord(
	t *testing.T,
	ctx context.Context,
	client *apiClient,
	collection, id, etag string,
	wantStatus int,
	destination any,
) {
	t.Helper()
	headers := make(http.Header)
	headers.Set("If-Match", etag)
	responseHeaders, err := client.requestWithHeaders(ctx, http.MethodDelete,
		relationE2ERecordPath(collection, id), nil, headers, wantStatus, destination)
	if err != nil {
		t.Fatal(err)
	}
	if responseHeaders.Get("PgBase-Compatibility") != "" {
		t.Fatalf("strict relation delete used compatibility path: headers=%v", responseHeaders)
	}
}

func relationE2ERecordPath(collection, id string) string {
	path := "/api/v1/collections/" + url.PathEscape(collection) + "/records"
	if id != "" {
		path += "/" + url.PathEscape(id)
	}
	return path
}

func (r relationE2ERecord) id(t *testing.T) string {
	t.Helper()
	return stringValue(t, r.data, "id")
}

func assertRelationE2EIDs(t *testing.T, record map[string]any, field string, want []string) {
	t.Helper()
	values, ok := record[field].([]any)
	if !ok {
		t.Fatalf("%s = %#v, want an ID array", field, record[field])
	}
	got := make([]string, len(values))
	for index, value := range values {
		id, ok := value.(string)
		if !ok {
			t.Fatalf("%s[%d] = %#v, want an ID", field, index, value)
		}
		got[index] = id
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%s IDs = %v, want %v", field, got, want)
	}
}

func assertRelationE2EExpansion(t *testing.T, record map[string]any, field string, wantIDs []string) {
	t.Helper()
	expand, ok := record["expand"].(map[string]any)
	if !ok {
		t.Fatalf("expand = %#v", record["expand"])
	}
	var values []any
	switch value := expand[field].(type) {
	case map[string]any:
		values = []any{value}
	case []any:
		values = value
	default:
		t.Fatalf("expand.%s = %#v", field, expand[field])
	}
	got := make([]string, len(values))
	for index, value := range values {
		target, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("expand.%s[%d] = %#v", field, index, value)
		}
		got[index] = stringValue(t, target, "id")
	}
	if !slices.Equal(got, wantIDs) {
		t.Fatalf("expanded %s IDs = %v, want %v", field, got, wantIDs)
	}
}

func assertRelationE2ENestedExpansion(
	t *testing.T,
	record map[string]any,
	parentField string,
	childField string,
	wantParentID string,
	wantChildID string,
) {
	t.Helper()
	expand, ok := record["expand"].(map[string]any)
	if !ok {
		t.Fatalf("expand = %#v", record["expand"])
	}
	parent, ok := expand[parentField].(map[string]any)
	if !ok || stringValue(t, parent, "id") != wantParentID {
		t.Fatalf("expand.%s = %#v, want parent %q", parentField, expand[parentField], wantParentID)
	}
	nested, ok := parent["expand"].(map[string]any)
	if !ok {
		t.Fatalf("expand.%s.expand = %#v", parentField, parent["expand"])
	}
	child, ok := nested[childField].(map[string]any)
	if !ok || stringValue(t, child, "id") != wantChildID {
		t.Fatalf("expand.%s.expand.%s = %#v, want child %q", parentField, childField, nested[childField], wantChildID)
	}
}

func waitForE2ECollections(t *testing.T, ctx context.Context, client *apiClient, names ...string) {
	t.Helper()
	if err := testsupport.Poll(ctx, 15*time.Second, 100*time.Millisecond, 3*time.Second, func(attemptCtx context.Context) (bool, error) {
		var catalog struct {
			Collections map[string]json.RawMessage `json:"collections"`
		}
		if err := client.request(attemptCtx, http.MethodGet, "/api/admin/collections", nil, http.StatusOK, &catalog); err != nil {
			return false, err
		}
		for _, name := range names {
			if _, ok := catalog.Collections[name]; !ok {
				return false, nil
			}
		}
		return true, nil
	}); err != nil {
		t.Fatalf("replica did not observe relation collections %v: %v", names, err)
	}
}
