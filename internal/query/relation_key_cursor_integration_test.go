//go:build integration

package query

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// A non-administrator ordering by a bare relation field must not learn the
// redacted target ids through nextCursor, and the anchor-only cursors that
// replace the value cursors must page through exactly the same rows.
func TestBareRelationSortCursorHidesRedactedTargetsAcrossPages(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{Name: "relkey_secrets"})
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{Name: "relkey_posts", Fields: []schema.FieldInput{
		{Name: "owner", Type: schema.TypeRelation, Relation: &schema.RelationInput{
			TargetCollection: "relkey_secrets", Cardinality: schema.RelationSingle, OnDelete: schema.RelationRestrict,
		}},
	}})
	secrets := make([]uuid.UUID, 3)
	for index := range secrets {
		created, err := records.Create(ctx, "relkey_secrets", nil)
		if err != nil {
			t.Fatal(err)
		}
		secrets[index] = uuid.MustParse(created["id"].(string))
	}
	// Owner groups span page boundaries at limit 2, and one row sorts as NULL.
	owners := map[uuid.UUID]*uuid.UUID{}
	for _, owner := range []*uuid.UUID{&secrets[0], &secrets[0], &secrets[0], &secrets[0], &secrets[1], &secrets[1], &secrets[1], &secrets[2], nil} {
		input := map[string]json.RawMessage{}
		if owner != nil {
			input["owner"] = json.RawMessage(`"` + owner.String() + `"`)
		}
		created, err := records.Create(ctx, "relkey_posts", input)
		if err != nil {
			t.Fatal(err)
		}
		owners[uuid.MustParse(created["id"].(string))] = owner
	}
	catalog, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	allow := accessrule.Rule{Op: "allow"}
	for _, name := range []string{"relkey_secrets", "relkey_posts"} {
		if _, err := recordaccess.NewService(pool).Set(ctx, name, true); err != nil {
			t.Fatal(err)
		}
		view := allow
		if name == "relkey_secrets" {
			view = accessrule.Rule{Op: "deny"}
		}
		raw, _ := json.Marshal(accessrule.Rules{List: allow, View: view, Create: allow, Update: allow, Delete: allow})
		if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.collection_rules(collection_id,rules) VALUES($1,$2)`, catalog.Collections[name].ID, raw); err != nil {
			t.Fatal(err)
		}
	}
	collectionID := catalog.Collections["relkey_posts"].ID
	administrator := Principal{Kind: identity.Administrator, ID: uuid.New()}
	anonymous := Principal{Kind: identity.Anonymous}
	assertNoTarget := func(label, token string) {
		t.Helper()
		raw, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range secrets {
			if bytes.Contains(raw, []byte(secret.String())) {
				t.Fatalf("%s cursor disclosed relation target %s: %q", label, secret, raw)
			}
		}
		decoded, err := records.cursors.Decode(token, collectionID, time.Now())
		if err != nil || !decoded.PrivateValues || len(decoded.Values) != 0 {
			t.Fatalf("%s cursor is not anchor-only: %#v err=%v", label, decoded, err)
		}
	}

	for _, sortText := range []string{"owner", "-owner", "-created_at,owner"} {
		all, err := records.List(ctx, ListRequest{Principal: administrator, Collection: "relkey_posts", Sort: sortText, Limit: 100})
		if err != nil || all.NextCursor != "" || len(all.Items) != len(owners) {
			t.Fatalf("%s administrator full page = %d items cursor %q err=%v", sortText, len(all.Items), all.NextCursor, err)
		}
		want := recordIDs(all.Items)

		got := []string{}
		request := ListRequest{Principal: anonymous, Collection: "relkey_posts", Sort: sortText, Limit: 2}
		for pages := 0; ; pages++ {
			if pages > len(owners) {
				t.Fatalf("%s pagination did not terminate", sortText)
			}
			page, err := records.List(ctx, request)
			if err != nil {
				t.Fatalf("%s anonymous page %d: %v", sortText, pages, err)
			}
			for _, item := range page.Items {
				if item["owner"] != nil {
					t.Fatalf("%s anonymous item exposed owner %v", sortText, item["owner"])
				}
			}
			got = append(got, recordIDs(page.Items)...)
			if page.NextCursor == "" {
				break
			}
			assertNoTarget(sortText, page.NextCursor)
			request.Cursor = page.NextCursor
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s anonymous anchor-only pages = %v, want %v", sortText, got, want)
		}

		// The administrator keeps the value cursor it always had.
		first, err := records.List(ctx, ListRequest{Principal: administrator, Collection: "relkey_posts", Sort: sortText, Limit: 1})
		if err != nil || first.NextCursor == "" {
			t.Fatalf("%s administrator first page err=%v", sortText, err)
		}
		decoded, err := records.cursors.Decode(first.NextCursor, collectionID, time.Now())
		if err != nil || decoded.PrivateValues || len(decoded.Values) == 0 {
			t.Fatalf("%s administrator cursor = %#v err=%v, want a value cursor", sortText, decoded, err)
		}
	}

	// A value cursor issued to the same principal before the anchor-only rule
	// is signed and already in the client's hands; it keeps continuing, and the
	// cursor it yields is anchor-only.
	request := ListRequest{Principal: anonymous, Collection: "relkey_posts", Sort: "owner", Limit: 2}
	first, err := records.List(ctx, request)
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first anonymous page err=%v", err)
	}
	request.Cursor = first.NextCursor
	expected, err := records.List(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := records.cursors.Decode(first.NextCursor, collectionID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ownerValue := json.RawMessage("null")
	if owner := owners[legacy.AnchorID]; owner != nil {
		ownerValue = json.RawMessage(`"` + owner.String() + `"`)
	}
	legacy.Values = []json.RawMessage{ownerValue, json.RawMessage(`"` + legacy.AnchorID.String() + `"`)}
	legacy.PrivateValues, legacy.AnchorHash = false, nil
	legacyToken, err := records.cursors.Encode(legacy)
	if err != nil {
		t.Fatal(err)
	}
	request.Cursor = legacyToken
	continued, err := records.List(ctx, request)
	if err != nil {
		t.Fatalf("legacy value cursor continuation: %v", err)
	}
	if !reflect.DeepEqual(recordIDs(continued.Items), recordIDs(expected.Items)) {
		t.Fatalf("legacy continuation = %v, want %v", recordIDs(continued.Items), recordIDs(expected.Items))
	}
	assertNoTarget("legacy continuation", continued.NextCursor)
}
