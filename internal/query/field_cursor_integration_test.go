//go:build integration

package query

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestPublicFieldComparisonsBindBothCursorOperands(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	const name = "field_cursor_records"
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{Name: name, Fields: []schema.FieldInput{
		{Name: "title", Type: schema.TypeText, Indexed: true},
		{Name: "label", Type: schema.TypeText},
		{Name: "expected", Type: schema.TypeText},
		{Name: "alternate", Type: schema.TypeText},
		{Name: "amount", Type: schema.TypeInteger},
		{Name: "minimum", Type: schema.TypeInteger},
		{Name: "other_minimum", Type: schema.TypeInteger},
		{Name: "flag", Type: schema.TypeBoolean},
		{Name: "expected_flag", Type: schema.TypeBoolean},
	}})
	for _, title := range []string{"alpha", "beta", "excluded"} {
		label, amount, flag := `"match"`, `2`, `true`
		if title == "excluded" {
			label, amount, flag = `"different"`, `0`, `false`
		}
		if _, err := records.Create(ctx, name, map[string]json.RawMessage{
			"title": json.RawMessage(`"` + title + `"`), "label": json.RawMessage(label),
			"expected": json.RawMessage(`"match"`), "alternate": json.RawMessage(`"match"`),
			"amount": json.RawMessage(amount), "minimum": json.RawMessage(`1`), "other_minimum": json.RawMessage(`1`),
			"flag": json.RawMessage(flag), "expected_flag": json.RawMessage(`true`),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := recordaccess.NewService(pool).Set(ctx, name, true); err != nil {
		t.Fatal(err)
	}
	catalog, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	collection := catalog.Collections[name]
	allow := accessrule.Rule{Op: "allow"}
	rules, err := json.Marshal(accessrule.Rules{List: allow, View: allow, Create: allow, Update: allow, Delete: allow})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.collection_rules(collection_id,rules) values($1,$2::jsonb)`, collection.ID, string(rules)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, filter string
		changed      []string
	}{
		{"text", `label = expected`, []string{`label = alternate`, `label = expected:lower`, `label:lower = expected`, `alternate = expected`}},
		{"integer", `amount >= minimum`, []string{`amount >= other_minimum`, `amount > minimum`, `minimum <= amount`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := ListRequest{Principal: Principal{Kind: identity.Anonymous}, Collection: name, Filter: tc.filter, Sort: "title", Limit: 1}
			first, err := records.List(ctx, request)
			if err != nil || len(first.Items) != 1 || first.Items[0]["title"] != "alpha" || first.NextCursor == "" {
				t.Fatalf("cursorless field comparison: page=%#v err=%v", first, err)
			}
			request.Cursor = first.NextCursor
			second, err := records.List(ctx, request)
			if err != nil || len(second.Items) != 1 || second.Items[0]["title"] != "beta" || second.NextCursor != "" {
				t.Fatalf("field comparison continuation: page=%#v err=%v", second, err)
			}
			for _, filter := range tc.changed {
				request.Filter = filter
				if _, err := records.List(ctx, request); !IsInvalidCursor(err) {
					t.Errorf("changed field comparison %q reused cursor: %v", filter, err)
				}
			}
			if tc.name == "text" {
				decoded, err := records.cursors.Decode(first.NextCursor, collection.ID, time.Time{})
				if err != nil {
					t.Fatal(err)
				}
				field, _ := filterField(collection, "label")
				legacy, err := json.Marshal(cursorV5FilterComparison{Kind: "comparison", Path: []string{field.ID.String()}, Type: string(field.Type), Operator: "=", Value: json.RawMessage(`""`)})
				if err != nil {
					t.Fatal(err)
				}
				decoded.V5.QueryHash = records.cursors.QueryFingerprint(append(append([]byte(`{"filter":`), legacy...), '}'))
				request.Cursor, err = records.cursors.Encode(decoded)
				if err != nil {
					t.Fatal(err)
				}
				request.Filter = tc.filter
				if _, err := records.List(ctx, request); !IsInvalidCursor(err) {
					t.Errorf("legacy ambiguous field cursor was accepted: %v", err)
				}
			}
		})
	}
	t.Run("contains_semantics", func(t *testing.T) {
		request := ListRequest{Principal: Principal{Kind: identity.Anonymous}, Collection: name, Filter: `label ~ 'match'`, Sort: "title", Limit: 1}
		first, err := records.List(ctx, request)
		if err != nil || first.NextCursor == "" {
			t.Fatalf("contains first page: page=%#v err=%v", first, err)
		}
		request.Cursor, request.Filter = first.NextCursor, `'match' ~ label`
		if _, err := records.List(ctx, request); !IsInvalidCursor(err) {
			t.Errorf("literal-left contains reused cursor: %v", err)
		}
		request.Filter, request.PocketBaseCompatibility = `label ~ 'match'`, true
		if _, err := records.List(ctx, request); !IsInvalidCursor(err) {
			t.Errorf("changed LIKE mode reused cursor: %v", err)
		}
	})
	t.Run("invalid_field_operators", func(t *testing.T) {
		for _, tc := range []struct{ filter, code string }{
			{`amount ~ minimum`, "type_mismatch"},
			{`amount !~ minimum`, "type_mismatch"},
			{`label:length = expected`, "type_mismatch"},
			{`label = expected:length`, "type_mismatch"},
			{`label:length ~ expected:length`, "type_mismatch"},
			{`amount ?> minimum`, "invalid_filter"},
			{`label ?= expected`, "invalid_filter"},
			{`flag ~ expected_flag`, "type_mismatch"},
			{`flag > expected_flag`, "type_mismatch"},
		} {
			request := ListRequest{Principal: Principal{Kind: identity.Anonymous}, Collection: name, Filter: tc.filter, Limit: 10}
			t.Run(tc.filter+"/list", func(t *testing.T) {
				_, err := records.List(ctx, request)
				assertValidationDetail(t, err, "filter", tc.code)
			})
			t.Run(tc.filter+"/count", func(t *testing.T) {
				_, err := Count(ctx, records, request)
				assertValidationDetail(t, err, "filter", tc.code)
			})
		}
	})
	t.Run("supported_field_operators", func(t *testing.T) {
		for _, tc := range []struct {
			filter string
			count  int
		}{
			{`amount = amount`, 3}, {`amount != minimum`, 3}, {`amount < minimum`, 1},
			{`amount <= minimum`, 1}, {`amount > minimum`, 2}, {`amount >= minimum`, 2},
			{`label = expected`, 2}, {`label != expected`, 1}, {`label < expected`, 1},
			{`label <= expected`, 3}, {`label > expected`, 0}, {`label >= expected`, 2},
			{`label ~ expected`, 2}, {`label !~ expected`, 1}, {`label:lower = expected:lower`, 2},
			{`label:length = expected:length`, 2}, {`label:length > expected:length`, 1},
			{`flag = expected_flag`, 2}, {`flag != expected_flag`, 1},
		} {
			t.Run(tc.filter, func(t *testing.T) {
				request := ListRequest{Principal: Principal{Kind: identity.Anonymous}, Collection: name, Filter: tc.filter, Limit: 10}
				page, err := records.List(ctx, request)
				if err != nil || len(page.Items) != tc.count {
					t.Fatalf("supported field comparison list: got %d err=%v want %d", len(page.Items), err, tc.count)
				}
				total, err := Count(ctx, records, request)
				if err != nil || total != int64(tc.count) {
					t.Fatalf("supported field comparison count: got %d err=%v want %d", total, err, tc.count)
				}
			})
		}
	})
	t.Run("literal_left_any_match_operators", func(t *testing.T) {
		for _, tc := range []struct {
			filter string
			count  int
		}{
			{`1 ?< amount`, 2}, {`1 ?<= amount`, 2}, {`1 ?> amount`, 1}, {`1 ?>= amount`, 1},
			{`'match' ?= label`, 2}, {`'match' ?!= label`, 1},
			{`'match' ?~ label`, 2}, {`'match' ?!~ label`, 1},
		} {
			t.Run(tc.filter, func(t *testing.T) {
				request := ListRequest{Principal: Principal{Kind: identity.Anonymous}, Collection: name, Filter: tc.filter, Limit: 10, PocketBaseCompatibility: true}
				page, err := records.List(ctx, request)
				if err != nil || len(page.Items) != tc.count {
					t.Fatalf("literal-left any-match list: got %d err=%v want %d", len(page.Items), err, tc.count)
				}
				total, err := Count(ctx, records, request)
				if err != nil || total != int64(tc.count) {
					t.Fatalf("literal-left any-match count: got %d err=%v want %d", total, err, tc.count)
				}
			})
		}
	})
}
