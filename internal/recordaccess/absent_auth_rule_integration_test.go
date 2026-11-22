//go:build integration

package recordaccess_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

type ruleProbe struct {
	collection schema.Collection
	pool       *database.Pool
}

func newRuleProbe(t *testing.T, name string, fields []schema.Field, ddl string) (context.Context, ruleProbe) {
	t.Helper()
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	collection := schema.Collection{ID: uuid.New(), Name: name, TableName: name, Fields: fields}
	if _, err = pool.Exec(ctx, `insert into pgbase_meta.collections(id,name,table_name,status,created_at,updated_at) values($1,$2,$3,'active',now(),now())`, collection.ID, collection.Name, collection.TableName); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	return ctx, ruleProbe{collection: collection, pool: pool}
}

// allows evaluates source as the view rule for one row. It reports an error
// (a 500 at the HTTP boundary) separately from a denial.
func (p ruleProbe) allows(ctx context.Context, t *testing.T, source string, principal identity.Subject, request *accessrule.RequestContext, id uuid.UUID) (bool, error) {
	t.Helper()
	rule, err := query.ParseAccessRule(source)
	if err != nil {
		t.Fatalf("%q: %v", source, err)
	}
	var allowed bool
	err = p.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var checkErr error
		allowed, checkErr = recordaccess.AllowsRecordInCatalogWithRules(ctx, tx, schema.Catalog{Collections: map[string]schema.Collection{p.collection.Name: p.collection}}, p.collection, accessrule.Rules{View: rule}, principal, "view", id, false, request)
		return checkErr
	})
	return allowed, err
}

func TestAbsentRequestAuthFieldsEvaluateAsNullAndFailClosed(t *testing.T) {
	fields := []schema.Field{{ID: uuid.New(), Name: "title", ColumnName: "title", Type: schema.TypeText}, {ID: uuid.New(), Name: "author", ColumnName: "author", Type: schema.TypeUUID}}
	ctx, probe := newRuleProbe(t, "absent_auth_notes", fields, `create table app.absent_auth_notes(id uuid primary key,title text,author uuid,created_at timestamptz default now(),updated_at timestamptz default now())`)
	userID, ownedID, nilOwnerID, ownerlessID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := probe.pool.Exec(ctx, `insert into app.absent_auth_notes(id,title,author) values($1,'mine',$4),($2,'nil owner',$5),($3,'no owner',null)`, ownedID, nilOwnerID, ownerlessID, userID, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	anonymous := identity.Subject{Kind: identity.Anonymous}
	guest := func() *accessrule.RequestContext {
		return &accessrule.RequestContext{Method: "GET", Auth: map[string]any{}}
	}
	user := identity.Subject{Kind: identity.User, ID: userID}
	member := func() *accessrule.RequestContext {
		// The auth values a record request carries (api.ruleRequestContext):
		// id, email, verified and created only. Profile fields such as "role"
		// are never loaded, so a missing key says nothing about the record.
		return &accessrule.RequestContext{Method: "GET", Auth: map[string]any{"id": userID, "email": "Member@Example.test", "verified": true}}
	}
	for _, test := range []struct {
		source    string
		principal identity.Subject
		request   func() *accessrule.RequestContext
		id        uuid.UUID
		want      bool
	}{
		// Absent keys compare as NULL instead of failing to compile.
		{"@request.auth.nickname = null", anonymous, guest, ownedID, true},
		{"@request.auth.nickname != null", anonymous, guest, ownedID, false},
		{"@request.auth.nickname = 'bob'", anonymous, guest, ownedID, false},
		{"@request.auth.nickname != 'bob'", anonymous, guest, ownedID, false},
		{"@request.auth.nickname ~ 'bo'", anonymous, guest, ownedID, false},
		{"@request.auth.verified = true", anonymous, guest, ownedID, false},
		{"@request.auth.verified = false", anonymous, guest, ownedID, false},
		{"@request.auth.score > 3", anonymous, guest, ownedID, false},
		{"@request.auth.score <= 3", anonymous, guest, ownedID, false},
		{"@request.auth.nickname = title", anonymous, guest, ownedID, false},
		{"title = @request.auth.nickname", anonymous, guest, ownedID, false},
		{"title != @request.auth.nickname", anonymous, guest, ownedID, false},
		// For a signed-in caller a key the request lacks is unknown: every
		// comparison fails closed, including "= null" (the caller may well
		// have a role in their auth record).
		{"@request.auth.role = null", user, member, ownedID, false},
		{"@request.auth.role = null || title = 'other'", user, member, ownedID, false},
		{"@request.auth.role != null", user, member, ownedID, false},
		{"@request.auth.role = 'admin'", user, member, ownedID, false},
		{"@request.auth.role != 'admin'", user, member, ownedID, false},
		{"title = @request.auth.role", user, member, ownedID, false},
		{"title != @request.auth.role", user, member, ownedID, false},
		{"@request.auth.verified = true", user, member, ownedID, true},
		{"@request.auth.email = 'Member@Example.test'", user, member, ownedID, true},
		// Modifiers on auth values are unsupported and fail closed for every
		// comparison, even when the key is present.
		{"@request.auth.email:lower = null", user, member, ownedID, false},
		{"@request.auth.email:lower != null", user, member, ownedID, false},
		{"@request.auth.email:lower = 'member@example.test'", user, member, ownedID, false},
		{"@request.auth.email:lower != 'x'", user, member, ownedID, false},
		{"@request.auth.nickname:lower = null", anonymous, guest, ownedID, false},
		{"title = @request.auth.email:lower", user, member, ownedID, false},
		{"title != @request.auth.email:lower", user, member, ownedID, false},
		// Ownership fails closed for guests, including a row holding the nil
		// UUID and a row with no owner.
		{"@request.auth.id = author", anonymous, guest, ownedID, false},
		{"@request.auth.id = author", anonymous, guest, nilOwnerID, false},
		{"@request.auth.id = author", anonymous, guest, ownerlessID, false},
		{"author = @request.auth.id", anonymous, guest, nilOwnerID, false},
		{"@request.auth.id = author", user, member, ownedID, true},
		{"@request.auth.id = author", user, member, nilOwnerID, false},
		// A guest's id is NULL, so a negative ownership test is false too.
		{"author != @request.auth.id", anonymous, guest, nilOwnerID, false},
		{"author != @request.auth.id", anonymous, guest, ownedID, false},
		{"author != @request.auth.id", user, member, nilOwnerID, true},
		// The authenticated guard still gates the null check.
		{"@request.auth.id != '' && @request.auth.nickname = null", anonymous, guest, ownedID, false},
		{"@request.auth.id != '' && @request.auth.nickname = null", user, member, ownedID, false},
		{"@request.auth.id = '' && @request.auth.nickname = null", anonymous, guest, ownedID, true},
		// Without a guard, a null check is satisfied by every guest.
		{"@request.auth.id != '' || @request.auth.nickname = null", anonymous, guest, ownedID, true},
		{"@request.auth.id != '' || @request.auth.verified = true", anonymous, guest, ownedID, false},
	} {
		got, err := probe.allows(ctx, t, test.source, test.principal, test.request(), test.id)
		if err != nil {
			t.Fatalf("%q principal=%s: %v", test.source, test.principal.Kind, err)
		}
		if got != test.want {
			t.Fatalf("%q principal=%s row=%s allowed=%t want=%t", test.source, test.principal.Kind, test.id, got, test.want)
		}
	}
}

func TestRequestSuppliedContainsPatternsMatchLiterally(t *testing.T) {
	fields := []schema.Field{{ID: uuid.New(), Name: "title", ColumnName: "title", Type: schema.TypeText}}
	ctx, probe := newRuleProbe(t, "contains_pattern_notes", fields, `create table app.contains_pattern_notes(id uuid primary key,title text,created_at timestamptz default now(),updated_at timestamptz default now())`)
	hello, percent, underscore, backslash := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := probe.pool.Exec(ctx, `insert into app.contains_pattern_notes(id,title) values($1,'hello world'),($2,'100% sure'),($3,'a_b'),($4,'back\slash')`, hello, percent, underscore, backslash); err != nil {
		t.Fatal(err)
	}
	rows := map[string]uuid.UUID{"hello": hello, "percent": percent, "underscore": underscore, "backslash": backslash}
	anonymous := identity.Subject{Kind: identity.Anonymous}
	request := func(value string) *accessrule.RequestContext {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return &accessrule.RequestContext{
			Method:  "GET",
			Query:   map[string][]string{"q": {value}},
			Headers: map[string][]string{"x_q": {value}},
			Body:    map[string]json.RawMessage{"title": raw},
			Present: map[string]bool{"title": true},
			Changed: map[string]bool{"title": true},
			Auth:    map[string]any{"nickname": value},
		}
	}
	for _, test := range []struct {
		source  string
		value   string
		allowed []string
	}{
		// A requester-supplied '%' or '_' used to widen the match to every row.
		{"title ~ @request.query.q", "%", []string{"percent"}},
		{"title ~ @request.query.q", "_", []string{"underscore"}},
		{"title ~ @request.query.q", "h%d", nil},
		{"title ~ @request.query.q", "h_llo", nil},
		{"title ~ @request.query.q", `\`, []string{"backslash"}},
		{"title ~ @request.query.q", "lo w", []string{"hello"}},
		{"title ~ @request.query.q:lower", "%", []string{"percent"}},
		{"title ~ @request.headers.x_q", "%", []string{"percent"}},
		{"title ~ @request.body.title", "%", []string{"percent"}},
		{"title ~ @request.auth.nickname", "%", []string{"percent"}},
		{"title ?~ @request.query.q:each", "%", []string{"percent"}},
		// A negated match can no longer be emptied with a wildcard.
		{"title !~ @request.query.q", "%", []string{"hello", "underscore", "backslash"}},
		{"title !~ @request.query.q", "_", []string{"hello", "percent", "backslash"}},
		// Patterns the rule author wrote keep their LIKE wildcards.
		{"title ~ 'h%d'", "", []string{"hello"}},
		{"title ~ 'h_llo'", "", []string{"hello"}},
		{"title !~ 'h%d'", "", []string{"percent", "underscore", "backslash"}},
	} {
		for name, id := range rows {
			got, err := probe.allows(ctx, t, test.source, anonymous, request(test.value), id)
			if err != nil {
				t.Fatalf("%q value=%q: %v", test.source, test.value, err)
			}
			if want := slices.Contains(test.allowed, name); got != want {
				t.Fatalf("%q value=%q row=%s allowed=%t want=%t", test.source, test.value, name, got, want)
			}
		}
	}
}
