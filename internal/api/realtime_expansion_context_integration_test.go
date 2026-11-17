//go:build integration

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/realtime"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/recordops"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestRealtimeExpansionUsesCurrentRequestPolicyContext(t *testing.T) {
	ctx := t.Context()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	administrator := validSession().Administrator
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.admins
		(id,email,password_hash,active,created_at,updated_at)
		values($1,$2,'test',true,current_timestamp,current_timestamp)`, administrator.ID, administrator.Email); err != nil {
		t.Fatal(err)
	}
	schemas := schema.NewService(pool, &schema.Cache{}, nil)
	for _, input := range []schema.CreateCollectionInput{
		{Name: "realtime_context_people", Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText, Required: true}}},
		{Name: "realtime_context_notes", Fields: []schema.FieldInput{
			{Name: "title", Type: schema.TypeText, Required: true},
			{Name: "owner", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "realtime_context_people", Cardinality: schema.RelationSingle, OnDelete: schema.RelationRestrict}},
		}},
	} {
		change := schema.NewCreateChange(input)
		plan, err := schemas.Preview(ctx, change)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := schemas.Apply(ctx, schema.LegacyApply(change, plan, administrator.ID)); err != nil {
			t.Fatal(err)
		}
		if _, err := recordaccess.NewService(pool).Set(ctx, input.Name, true); err != nil {
			t.Fatal(err)
		}
	}
	records := recordops.NewService(pool, schemas, []byte("0123456789abcdef0123456789abcdef"), nil, nil, nil)
	admin := identity.Subject{Kind: identity.Administrator, ID: administrator.ID}
	person, err := records.Create(ctx, recordops.CreateRequest{Principal: admin, RequestID: "realtime-context-person", Collection: "realtime_context_people", Input: map[string]json.RawMessage{"name": json.RawMessage(`"Ada"`)}})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := json.Marshal(person.Record.Data["id"])
	if err != nil {
		t.Fatal(err)
	}
	note, err := records.Create(ctx, recordops.CreateRequest{Principal: admin, RequestID: "realtime-context-note", Collection: "realtime_context_notes", Input: map[string]json.RawMessage{"title": json.RawMessage(`"Visible note"`), "owner": owner}})
	if err != nil {
		t.Fatal(err)
	}
	allow := accessrule.Rule{Op: "allow"}
	contextRule, err := query.ParseAccessRule("@request.headers.x_scope = 'allowed'")
	if err != nil {
		t.Fatal(err)
	}
	rules := recordaccess.NewRuleService(pool, schemas)
	if _, err := rules.Set(ctx, "realtime_context_people", accessrule.Rules{List: allow, View: contextRule, Create: allow, Update: allow, Delete: allow}); err != nil {
		t.Fatal(err)
	}
	if _, err := rules.Set(ctx, "realtime_context_notes", accessrule.Rules{List: allow, View: allow, Create: allow, Update: allow, Delete: allow}); err != nil {
		t.Fatal(err)
	}
	guest := identity.Subject{Kind: identity.Anonymous}
	hub := newRealtimeHub(Dependencies{Records: records})
	message := realtimePendingMessage{
		subscription: realtimeSubscription{raw: "realtime_context_notes?expand=owner", collection: "realtime_context_notes", expand: "owner"},
		event:        realtime.Event{Collection: "realtime_context_notes", Operation: "create", PublicID: note.Record.Data["id"].(string), Record: note.Record.Data},
	}
	for _, scope := range []string{"allowed", "denied"} {
		t.Run(scope, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/realtime", nil)
			request.Header.Set("X-Scope", scope)
			request = request.WithContext(context.WithValue(ctx, requestIDKey{}, "realtime-context-read"))
			direct, err := records.Get(ctx, recordops.GetRequest{Principal: guest, RequestID: "realtime-context-direct", Collection: message.event.Collection, PublicID: message.event.PublicID, Expand: "owner", RuleContext: ruleRequestContext(request)})
			if err != nil {
				t.Fatal(err)
			}
			if scope == "allowed" {
				expand, ok := direct.Data["expand"].(map[string]any)
				if !ok || expand["owner"] == nil {
					t.Fatalf("fixture direct read lost allowed owner: %#v", direct.Data)
				}
			}
			streamed, included, err := hub.realtimeMessageRecord(request, guest, message)
			if err != nil || !included {
				t.Fatalf("realtime record included=%v err=%v", included, err)
			}
			if !reflect.DeepEqual(streamed["expand"], direct.Data["expand"]) {
				t.Fatalf("realtime expansion differs from same-context direct read: stream=%#v direct=%#v", streamed["expand"], direct.Data["expand"])
			}
		})
	}
}
