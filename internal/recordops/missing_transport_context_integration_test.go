//go:build integration

package recordops

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
)

func TestRecordMutationMissingTransportContextFailsClosed(t *testing.T) {
	f := newUserAccessFixture(t)
	f.enable(t, "records")
	ctx := t.Context()
	allow := accessrule.Rule{Op: "allow"}
	for _, test := range []struct {
		name, expression string
		metadata         *accessrule.RequestContext
	}{
		{"method", `@request.method != 'GET'`, nil},
		{"header", `@request.headers.x_scope != 'blocked'`, nil},
		{"query", `@request.query.scope != 'blocked'`, nil},
		{"nested_alternative", `title != '' || @request.method != 'GET'`, nil},
		{"right_operand", `title != @request.method`, nil},
		{"empty_metadata", `@request.method != 'GET'`, &accessrule.RequestContext{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := f.create(t, f.admin, "existing-"+test.name, "")
			rule, err := query.ParseAccessRule(test.expression)
			if err != nil {
				t.Fatal(err)
			}
			rules := accessrule.Rules{List: allow, View: allow, Create: rule, Update: rule, Delete: allow}
			if _, err := recordaccess.NewRuleService(f.pool, f.schemas).Set(ctx, "records", rules); err != nil {
				t.Fatal(err)
			}
			input := map[string]json.RawMessage{"title": json.RawMessage(`"denied-` + test.name + `"`)}
			if _, err := f.records.Create(ctx, CreateRequest{Principal: f.first, RequestID: "missing-create-" + test.name, Collection: "records", Input: input, RuleContext: test.metadata}); !errors.Is(err, recordaccess.ErrDenied) {
				t.Errorf("create without trusted transport context = %v; want denied", err)
			}
			if _, err := f.records.Update(ctx, UpdateRequest{Principal: f.first, RequestID: "missing-update-" + test.name, Collection: "records", ID: original.InternalID, Input: map[string]json.RawMessage{"count": json.RawMessage(`7`)}, RuleContext: test.metadata}); !errors.Is(err, query.ErrRecordNotFound) {
				t.Errorf("update without trusted transport context = %v; want record not found", err)
			}
			var created, count int
			if err := f.pool.QueryRow(ctx, `select count(*) from app.records where title=$1`, "denied-"+test.name).Scan(&created); err != nil || created != 0 {
				t.Errorf("denied create persisted %d records, err=%v", created, err)
			}
			if err := f.pool.QueryRow(ctx, `select count from app.records where id=$1`, original.InternalID).Scan(&count); err != nil || count != 0 {
				t.Errorf("denied update persisted count=%d, err=%v", count, err)
			}
		})
	}
}

func TestBatchMissingTransportContextRollsBackAllArtifacts(t *testing.T) {
	f := newUserAccessFixture(t)
	f.enable(t, "records")
	ctx := t.Context()
	allow := accessrule.Rule{Op: "allow"}
	rule, err := query.ParseAccessRule(`@request.method != 'GET'`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = recordaccess.NewRuleService(f.pool, f.schemas).Set(ctx, "records", accessrule.Rules{List: allow, View: allow, Create: rule, Update: allow, Delete: allow}); err != nil {
		t.Fatal(err)
	}
	_, err = f.records.Batch(ctx, BatchRequest{Principal: f.first, RequestID: "batch-missing-transport", Operations: []BatchOperation{{Create: &CreateRequest{Collection: "records", Input: map[string]json.RawMessage{"title": json.RawMessage(`"forbidden-batch"`)}}}}})
	if !errors.Is(err, recordaccess.ErrDenied) {
		t.Errorf("batch without trusted transport context = %v; want denied", err)
	}
	for _, table := range []string{"app.records", "pgbase_meta.record_revisions", "pgbase_meta.record_audit_events", "pgbase_meta.record_outbox"} {
		var count int
		if err := f.pool.QueryRow(ctx, "select count(*) from "+table).Scan(&count); err != nil || count != 0 {
			t.Errorf("%s rows after denied batch = %d, err=%v", table, count, err)
		}
	}
}

func TestRecordMutationTransportControlsAndBodyOnlyRules(t *testing.T) {
	f := newUserAccessFixture(t)
	f.enable(t, "records")
	ctx := t.Context()
	allow := accessrule.Rule{Op: "allow"}
	for _, test := range []struct {
		name, expression string
		metadata         *accessrule.RequestContext
		allowed          bool
	}{
		{"real_method_allowed", `@request.method = 'POST'`, &accessrule.RequestContext{Method: "POST"}, true},
		{"real_method_denied", `@request.method != 'GET'`, &accessrule.RequestContext{Method: "GET"}, false},
		{"real_header_allowed", `@request.headers.x_scope = 'write'`, &accessrule.RequestContext{Method: "POST", Headers: map[string][]string{"x_scope": {"write"}}}, true},
		{"real_header_denied", `@request.headers.x_scope != 'blocked'`, &accessrule.RequestContext{Method: "POST", Headers: map[string][]string{"x_scope": {"blocked"}}}, false},
		{"real_header_absent", `@request.headers.x_scope != 'blocked'`, &accessrule.RequestContext{Method: "POST"}, true},
		{"real_query_allowed", `@request.query.scope = 'write'`, &accessrule.RequestContext{Method: "POST", Query: map[string][]string{"scope": {"write"}}}, true},
		{"real_query_denied", `@request.query.scope != 'blocked'`, &accessrule.RequestContext{Method: "POST", Query: map[string][]string{"scope": {"blocked"}}}, false},
		{"embedded_body", `@request.body.title:isset = true`, nil, true},
		{"embedded_changed", `@request.body.title:changed = true`, nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			rule, err := query.ParseAccessRule(test.expression)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := recordaccess.NewRuleService(f.pool, f.schemas).Set(ctx, "records", accessrule.Rules{List: allow, View: allow, Create: rule, Update: allow, Delete: allow}); err != nil {
				t.Fatal(err)
			}
			_, err = f.records.Create(ctx, CreateRequest{Principal: f.first, RequestID: "transport-control-" + test.name, Collection: "records", Input: map[string]json.RawMessage{"title": json.RawMessage(`"` + test.name + `"`)}, RuleContext: test.metadata})
			if test.allowed && err != nil || !test.allowed && !errors.Is(err, recordaccess.ErrDenied) {
				t.Fatalf("allowed=%v, err=%v", test.allowed, err)
			}
		})
	}
}

func TestRecordCreateReplayRequiresTransportContext(t *testing.T) {
	f := newUserAccessFixture(t)
	f.enable(t, "records")
	ctx := t.Context()
	allow := accessrule.Rule{Op: "allow"}
	rule, err := query.ParseAccessRule(`@request.method != 'GET'`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recordaccess.NewRuleService(f.pool, f.schemas).Set(ctx, "records", accessrule.Rules{List: allow, View: allow, Create: rule, Update: allow, Delete: allow}); err != nil {
		t.Fatal(err)
	}
	request := CreateRequest{Principal: f.first, RequestID: "replay-original", Collection: "records", IdempotencyKey: "transport-replay-key", Input: map[string]json.RawMessage{"title": json.RawMessage(`"replay-owned"`)}, RuleContext: &accessrule.RequestContext{Method: "POST"}}
	created, err := f.records.Create(ctx, request)
	if err != nil || created.Replayed {
		t.Fatalf("original create replayed=%v err=%v", created.Replayed, err)
	}
	request.RequestID, request.RuleContext = "replay-missing-transport", nil
	if _, err := f.records.Create(ctx, request); !errors.Is(err, recordaccess.ErrDenied) {
		t.Fatalf("replay without trusted metadata = %v; want denied", err)
	}
	request.RequestID, request.RuleContext = "replay-authorized", &accessrule.RequestContext{Method: "POST"}
	replayed, err := f.records.Create(ctx, request)
	if err != nil || !replayed.Replayed || replayed.InternalID != created.InternalID {
		t.Fatalf("authorized replay=%+v err=%v", replayed, err)
	}
	for _, table := range []string{"app.records", "pgbase_meta.record_revisions", "pgbase_meta.record_audit_events", "pgbase_meta.record_outbox"} {
		var count int
		if err := f.pool.QueryRow(ctx, "select count(*) from "+table).Scan(&count); err != nil || count != 1 {
			t.Errorf("%s rows after authorized replay = %d, err=%v", table, count, err)
		}
	}
}
