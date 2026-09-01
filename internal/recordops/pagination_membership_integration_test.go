//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/schema"
	"github.com/webisnoteasy/PgBase/tests/testsupport"
)

const r0DelayedCreateLockKey int64 = 5_922_338_308_289_414_401

type r0CreateResult struct {
	result MutationResult
	err    error
}

func TestCurrentKeysetTraversalMixesLaterCommitAfterAnchorAndOmitsBeforeAnchor(t *testing.T) {
	_, pool, schemas, records, principal := newRecordOperationsTestWithURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	collection := applyDeleteCollection(t, ctx, schemas, principal.ID, schema.CreateCollectionInput{
		Name: "r0_page_membership",
		Fields: []schema.FieldInput{
			{Name: "name", Type: schema.TypeText, Required: true, Unique: true},
		},
	})

	baseline := []struct {
		name      string
		createdAt string
	}{
		{name: "010", createdAt: "2020-01-01 00:00:10+00"},
		{name: "020", createdAt: "2020-01-01 00:00:20+00"},
		{name: "030", createdAt: "2020-01-01 00:00:30+00"},
	}
	for _, fixture := range baseline {
		created, err := records.Create(ctx, CreateRequest{
			Principal: principal, RequestID: "r0-page-create-" + fixture.name, Collection: collection.Name,
			Input: map[string]json.RawMessage{"name": json.RawMessage(`"` + fixture.name + `"`)},
		})
		if err != nil {
			t.Fatalf("create baseline %s: %v", fixture.name, err)
		}
		if _, err := pool.Exec(ctx, `UPDATE app.r0_page_membership
			SET created_at = $1::timestamptz, updated_at = $1::timestamptz WHERE id = $2`,
			fixture.createdAt, recordResultID(t, created)); err != nil {
			t.Fatalf("timestamp baseline %s: %v", fixture.name, err)
		}
	}

	if _, err := pool.Exec(ctx, `CREATE SEQUENCE pgbase_meta.r0_delayed_create_ready`); err != nil {
		t.Fatal(err)
	}
	controlFunction := fmt.Sprintf(`CREATE FUNCTION pgbase_meta.r0_control_page_membership_create()
		RETURNS trigger LANGUAGE plpgsql AS $function$
		BEGIN
			IF NEW.name = '001' THEN
				NEW.created_at = '2020-01-01 00:00:01+00'::timestamptz;
				NEW.updated_at = NEW.created_at;
				PERFORM nextval('pgbase_meta.r0_delayed_create_ready');
				PERFORM pg_advisory_xact_lock(%d);
			ELSIF NEW.name = '015' THEN
				NEW.created_at = '2020-01-01 00:00:15+00'::timestamptz;
				NEW.updated_at = NEW.created_at;
			END IF;
			RETURN NEW;
		END
		$function$`, r0DelayedCreateLockKey)
	if _, err := pool.Exec(ctx, controlFunction); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TRIGGER r0_control_page_membership_create
		BEFORE INSERT ON app.r0_page_membership FOR EACH ROW
		EXECUTE FUNCTION pgbase_meta.r0_control_page_membership_create()`); err != nil {
		t.Fatal(err)
	}

	lockConnection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lockConnection.Release()
	if _, err := lockConnection.Exec(ctx, "SELECT pg_advisory_lock($1)", r0DelayedCreateLockKey); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			var unlocked bool
			if err := lockConnection.QueryRow(context.Background(), "SELECT pg_advisory_unlock($1)", r0DelayedCreateLockKey).Scan(&unlocked); err != nil {
				t.Errorf("release delayed-create lock: %v", err)
			}
		}
	}()

	delayed := make(chan r0CreateResult, 1)
	go func() {
		result, err := records.Create(ctx, CreateRequest{
			Principal: principal, RequestID: "r0-page-create-001", Collection: collection.Name,
			Input: map[string]json.RawMessage{"name": json.RawMessage(`"001"`)},
		})
		delayed <- r0CreateResult{result: result, err: err}
	}()
	if err := testsupport.Poll(ctx, 3*time.Second, 5*time.Millisecond, 100*time.Millisecond, func(attempt context.Context) (bool, error) {
		var ready bool
		err := pool.QueryRow(attempt, `SELECT is_called FROM pgbase_meta.r0_delayed_create_ready`).Scan(&ready)
		return ready, err
	}); err != nil {
		t.Fatalf("wait for delayed create: %v", err)
	}

	first, err := records.List(ctx, ListRequest{
		Principal: principal, RequestID: "r0-page-first", Collection: collection.Name, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := r0PageNames(t, first.Items); !reflect.DeepEqual(got, []string{"010"}) || first.NextCursor == "" {
		t.Fatalf("first page = %v cursor:%t, want [010] and a cursor", got, first.NextCursor != "")
	}

	later := make(chan r0CreateResult, 1)
	go func() {
		result, err := records.Create(ctx, CreateRequest{
			Principal: principal, RequestID: "r0-page-create-015", Collection: collection.Name,
			Input: map[string]json.RawMessage{"name": json.RawMessage(`"015"`)},
		})
		later <- r0CreateResult{result: result, err: err}
	}()
	var unlocked bool
	if err := lockConnection.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", r0DelayedCreateLockKey).Scan(&unlocked); err != nil || !unlocked {
		t.Fatalf("release delayed-create lock = %t, %v", unlocked, err)
	}
	locked = false
	for name, resultChannel := range map[string]<-chan r0CreateResult{"001": delayed, "015": later} {
		select {
		case created := <-resultChannel:
			if created.err != nil || created.result.Record == nil {
				t.Fatalf("create %s result/error = %#v/%v", name, created.result, created.err)
			}
		case <-ctx.Done():
			t.Fatalf("create %s did not finish: %v", name, ctx.Err())
		}
	}

	continued, err := records.List(ctx, ListRequest{
		Principal: principal, RequestID: "r0-page-continued", Collection: collection.Name,
		Cursor: first.NextCursor, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := r0PageNames(t, continued.Items); !reflect.DeepEqual(got, []string{"015", "020", "030"}) {
		t.Fatalf("continued page = %v, want current mixed membership [015 020 030]", got)
	}

	fresh, err := records.List(ctx, ListRequest{
		Principal: principal, RequestID: "r0-page-fresh", Collection: collection.Name, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := r0PageNames(t, fresh.Items); !reflect.DeepEqual(got, []string{"001", "010", "015", "020", "030"}) {
		t.Fatalf("fresh page = %v, want all five committed records", got)
	}
}

func r0PageNames(t *testing.T, items []map[string]any) []string {
	t.Helper()
	names := make([]string, len(items))
	for index, item := range items {
		name, ok := item["name"].(string)
		if !ok {
			t.Fatalf("item %d name = %#v", index, item["name"])
		}
		names[index] = name
	}
	return names
}
