//go:build integration

package query

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/recordartifact"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// TestGetReadsRevisionWithTheRecordRow pins the single-record read to one
// statement for the row and its revision: the ETag and version must equal
// what a separate record_revisions read produced, with and without a
// revision row, for an administrator and for a rule-filtered public reader
// whose policy arguments follow the revision placeholder.
func TestGetReadsRevisionWithTheRecordRow(t *testing.T) {
	databaseURL, pool, schemas, records := newR0QueryServiceTest(t)
	ctx := context.Background()
	collection := applyR0QueryCollection(t, schemas, schema.CreateCollectionInput{
		Name: "get_revision_records",
		Fields: []schema.FieldInput{
			{Name: "title", Type: schema.TypeText, Required: true},
			// Names that a joined revision table would make ambiguous.
			{Name: "collection_id", Type: schema.TypeText},
			{Name: "record_id", Type: schema.TypeText},
		},
	})
	allow := accessrule.Rule{Op: "allow"}
	view := accessrule.Rule{Op: "eq", Field: "title", Value: json.RawMessage(`"visible"`)}
	rules, err := json.Marshal(accessrule.Rules{List: allow, View: view, Create: allow, Update: allow, Delete: allow})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recordaccess.NewService(pool).Set(ctx, collection.Name, true); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.collection_rules(collection_id,rules) VALUES($1,$2::jsonb)`, collection.ID, string(rules)); err != nil {
		t.Fatal(err)
	}
	revised := uuid.MustParse("01999222-2222-7222-8222-222222222221")
	unrevised := uuid.MustParse("01999222-2222-7222-8222-222222222222")
	hidden := uuid.MustParse("01999222-2222-7222-8222-222222222223")
	for id, title := range map[uuid.UUID]string{revised: "visible", unrevised: "visible", hidden: "hidden"} {
		if _, err := pool.Exec(ctx, `INSERT INTO `+quotedTable(collection)+` (id, created_at, updated_at, title, collection_id, record_id)
			VALUES ($1, transaction_timestamp(), transaction_timestamp(), $2, 'c', 'r')`, id, title); err != nil {
			t.Fatal(err)
		}
	}
	eventID := uuid.MustParse("01999222-2222-7222-8222-222222222224")
	validator := bytes.Repeat([]byte{7}, 32)
	digest, err := recordartifact.SchemaDigest(collection)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The compiled catalog shares one derived value; its digest is the one
	// SchemaDigest computes, and a later schema version starts afresh.
	shared := derivedCatalog(catalog)
	if derivedCatalog(catalog) != shared {
		t.Fatal("compiled catalog did not share its derived read metadata")
	}
	for range 2 {
		if cached, err := shared.schemaDigest(catalog.Collections[collection.Name]); err != nil || cached != digest {
			t.Fatalf("cached schema digest = %x, %v; want %x", cached, err, digest)
		}
	}
	if found, ok := catalogCollectionByID(catalog, collection.ID); !ok || found.Name != collection.Name {
		t.Fatalf("catalogCollectionByID = %#v, %t", found, ok)
	}
	wantRevisedETag := recordartifact.RevisionETag(collection.ID, revised, digest, validator)
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.record_audit_events
		(event_id, actor_kind, actor_id, request_id, collection_id, collection_name, record_id,
		 operation, schema_version, schema_digest, previous_version, new_version, previous_etag,
		 new_etag, changed_fields, input_hash, idempotency_key_hash, compatibility, committed_at)
		VALUES ($1, 'administrator', $2, 'revision_get', $3, $4, $5,
		 'create', $6, $7, NULL, 1, NULL, $8, ARRAY['title'], $9, NULL,
		 'missing-idempotency-key', transaction_timestamp())`,
		eventID, uuid.MustParse(r0AdministratorID), collection.ID, collection.Name, revised,
		catalog.Version, digest[:], wantRevisedETag, bytes.Repeat([]byte{8}, 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.record_revisions
		(collection_id, record_id, version, validator, last_event_id, updated_at)
		VALUES ($1, $2, 1, $3, $4, transaction_timestamp())`, collection.ID, revised, validator, eventID); err != nil {
		t.Fatal(err)
	}
	syncMembershipOrdinals(t, ctx, pool)

	tracer := &readKernelQueryTracer{}
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.Tracer = tracer
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	get := func(principal Principal, id uuid.UUID) (Record, []string, error) {
		t.Helper()
		tx, err := conn.BeginTx(ctx, database.ReadSnapshotOptions())
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		tracer.start()
		record, err := records.Get(database.WithTransaction(ctx, tx), GetRequest{Principal: principal, Collection: collection.Name, ID: id})
		return record, tracer.stop(), err
	}
	administrator := Principal{Kind: PrincipalAdministrator, ID: uuid.MustParse(r0AdministratorID)}
	anonymous := Principal{Kind: identity.Anonymous}

	for _, principal := range []Principal{administrator, anonymous} {
		record, statements, err := get(principal, revised)
		if err != nil {
			t.Fatalf("%s Get revised: %v", principal.Kind, err)
		}
		if record.Version != 1 || record.ETag != wantRevisedETag || record.Data["title"] != "visible" || record.Data["collection_id"] != "c" {
			t.Fatalf("%s Get revised = %#v, want version 1 and %s", principal.Kind, record, wantRevisedETag)
		}
		revisionReads, rootReads := 0, 0
		for _, statement := range statements {
			if strings.Contains(statement, "pgbase_meta.record_revisions") {
				revisionReads++
			}
			if strings.Contains(statement, `FROM "app"."get_revision_records"`) {
				rootReads++
				if !strings.Contains(statement, "pgbase_meta.record_revisions") {
					t.Fatalf("%s root read omitted the revision: %s", principal.Kind, statement)
				}
			}
		}
		if revisionReads != 1 || rootReads != 1 {
			t.Fatalf("%s Get used %d revision and %d root statements:\n%s", principal.Kind, revisionReads, rootReads, formatQueryTrace(statements))
		}

		record, _, err = get(principal, unrevised)
		if err != nil {
			t.Fatalf("%s Get unrevised: %v", principal.Kind, err)
		}
		root := map[string]any{}
		for key, value := range record.Data {
			root[key] = value
		}
		wantLegacyETag, err := recordartifact.ETag(collection.ID, unrevised, digest, root, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		if record.Version != 0 || record.ETag != wantLegacyETag {
			t.Fatalf("%s Get unrevised = %#v, want version 0 and %s", principal.Kind, record, wantLegacyETag)
		}
	}
	if _, _, err := get(anonymous, hidden); err != ErrRecordNotFound {
		t.Fatalf("rule-hidden Get error = %v, want ErrRecordNotFound", err)
	}
	if _, _, err := get(administrator, uuid.MustParse("01999222-2222-7222-8222-222222222229")); err != ErrRecordNotFound {
		t.Fatalf("missing Get error = %v, want ErrRecordNotFound", err)
	}

	// A schema change publishes a new compiled catalog whose digest differs;
	// the read must use it rather than anything derived from the old one.
	applyR0QueryCollection(t, schemas, schema.CreateCollectionInput{
		Name: "get_revision_other", Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText}},
	})
	next, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if next.Version == catalog.Version || derivedCatalog(next) == shared {
		t.Fatalf("new catalog version %d reused derived metadata of %d", next.Version, catalog.Version)
	}
	record, _, err := get(administrator, revised)
	if err != nil || record.ETag != wantRevisedETag {
		t.Fatalf("Get after schema change = %#v, %v", record, err)
	}
}
