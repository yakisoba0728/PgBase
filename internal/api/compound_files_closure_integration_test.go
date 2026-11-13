//go:build integration

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/jobs"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/recordops"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestMultipartBatch65FilesCommitAndAtomicRollback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint("rollback=", fail), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			if err := migrate.New(pool, nil).Up(ctx); err != nil {
				t.Fatal(err)
			}
			admin := validSession().Administrator
			if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.admins(id,email,password_hash,active,created_at,updated_at) VALUES($1,$2,'fixture',true,now(),now())`, admin.ID, admin.Email); err != nil {
				t.Fatal(err)
			}
			schemas := schema.NewService(pool, &schema.Cache{}, nil)
			maximum := 65
			change := schema.NewCreateChange(schema.CreateCollectionInput{Name: "compound_files", Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText, Required: true}, {Name: "attachments", Type: schema.TypeFile, Options: &schema.FieldOptions{MaxSelect: &maximum}}}})
			plan, err := schemas.Preview(ctx, change)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := schemas.Apply(ctx, schema.LegacyApply(change, plan, admin.ID)); err != nil {
				t.Fatal(err)
			}
			secret := bytes.Repeat([]byte{8}, 32)
			objects, err := filestore.NewStorageController(ctx, pool, secret, filestore.StorageConfig{Driver: "local", LocalDirectory: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			files, err := filestore.NewService(pool, objects, func(context.Context, pgx.Tx, identity.Subject, uuid.UUID, uuid.UUID, string, filestore.Operation) error {
				return nil
			}, filestore.Options{MaximumBytes: 1024, MaximumImagePixels: 1000, ThumbnailDimension: 64, AllowedTypes: []string{"text/plain"}, TokenSecret: secret, TokenTTL: time.Minute, Policy: func(context.Context, pgx.Tx, uuid.UUID, string) (filestore.FieldPolicy, error) {
				return filestore.FieldPolicy{MaximumCount: 65}, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			records := recordops.NewService(pool, schemas, secret, nil, nil, nil)
			deps := recordTestDependencies(&fakeAdminAuth{authenticateAdmin: admin}, records)
			deps.Files = files
			deps.MaximumFileUploadBytes = 1024
			handler := mustRouter(t, deps)
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			refs := make([]map[string]string, 65)
			for i := range refs {
				refs[i] = map[string]string{"$file": fmt.Sprint("file", i)}
			}
			operations := []any{map[string]any{"operation": "create", "collection": "compound_files", "input": map[string]any{"title": "kept", "attachments": refs}}}
			if fail {
				if _, err := pool.Exec(ctx, `CREATE SEQUENCE pgbase_meta.fixture_file_attempts;
 CREATE FUNCTION pgbase_meta.fixture_file_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 PERFORM nextval('pgbase_meta.fixture_file_attempts');
 IF NEW.filename='file64.txt' THEN RAISE EXCEPTION 'fixture file publication failure'; END IF;
 RETURN NEW; END $$;
 CREATE TRIGGER fixture_file_failure BEFORE INSERT ON pgbase_meta.record_files FOR EACH ROW EXECUTE FUNCTION pgbase_meta.fixture_file_failure()`); err != nil {
					t.Fatal(err)
				}
			}
			requests, err := writer.CreateFormField("requests")
			if err != nil {
				t.Fatal(err)
			}
			if err := json.NewEncoder(requests).Encode(map[string]any{"operations": operations}); err != nil {
				t.Fatal(err)
			}
			for i := range refs {
				header := textproto.MIMEHeader{"Content-Disposition": {fmt.Sprintf(`form-data; name="file%d"; filename="file%d.txt"`, i, i)}, "Content-Type": {"text/plain"}}
				part, err := writer.CreatePart(header)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := io.WriteString(part, "tiny"); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			r := authenticatedRecordRequest(http.MethodPost, "/api/v1/collections/batch", body.String(), true).WithContext(ctx)
			r.Header.Set("Content-Type", writer.FormDataContentType())
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if ctx.Err() != nil {
				t.Fatalf("65-part request exhausted its own leases: %v", ctx.Err())
			}
			if !fail && w.Code != 200 || fail && w.Code < 400 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			var recordsCount, ready, queued int
			for _, query := range []struct {
				sql string
				out *int
			}{{"select count(*) from app.compound_files", &recordsCount}, {"select count(*) from pgbase_meta.record_files where status='ready'", &ready}, {"select count(*) from pgbase_meta.file_object_deletions", &queued}} {
				if err := pool.QueryRow(ctx, query.sql).Scan(query.out); err != nil {
					t.Fatal(err)
				}
			}
			if !fail && (recordsCount != 1 || ready != 65 || queued != 0) {
				t.Fatalf("commit records=%d ready=%d queued=%d", recordsCount, ready, queued)
			}
			if fail && (recordsCount != 0 || ready != 0 || queued != 65) {
				t.Fatalf("rollback records=%d ready=%d queued=%d", recordsCount, ready, queued)
			}
			if fail {
				var attempts int
				if err := pool.QueryRow(ctx, `select last_value from pgbase_meta.fixture_file_attempts`).Scan(&attempts); err != nil {
					t.Fatal(err)
				}
				if attempts != 65 {
					t.Fatalf("failure did not reach the 65th transactional file insert: %d", attempts)
				}
			}
			// Handler cleanup releases the shared admission and object-ownership session.
			var locks int
			if err := pool.QueryRow(ctx, `select count(*) from pg_locks where locktype='advisory' and mode='ShareLock' and granted and database=(select oid from pg_database where datname=current_database()) and classid=($1::bigint >> 32)::oid and objid=($1::bigint & 4294967295)::oid and objsubid=1`, int64(0x7067626173650034)).Scan(&locks); err != nil {
				t.Fatal(err)
			}
			if locks != 0 {
				t.Fatalf("retained shared storage locks=%d", locks)
			}
		})
	}
}

func TestConfigureJobOmittedTimezonePreservesExistingZone(t *testing.T) {
	pool, err := database.Open(t.Context(), testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.New(pool, nil).Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	service := jobs.NewService(pool, jobs.NewRegistry(), nil)
	if err := service.Register(t.Context(), "fixture.timezone", "0 9 * * 1", "Asia/Seoul", true, func(context.Context, jobs.Job) error { return nil }); err != nil {
		t.Fatal(err)
	}
	items, err := service.ListJobs(t.Context())
	if err != nil || len(items) != 1 {
		t.Fatalf("jobs=%v err=%v", items, err)
	}
	id := items[0].ID
	deps := testDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, mustURL("https://admin.example.com"))
	deps.Jobs = service
	handler := mustRouter(t, deps)
	for _, test := range []struct {
		body, zone string
		enabled    bool
	}{{`{"schedule":"0 9 * * 1","enabled":false}`, "Asia/Seoul", false}, {`{"schedule":"0 10 * * 1","enabled":true}`, "Asia/Seoul", true}, {`{"schedule":"0 10 * * 1","timezone":"UTC","enabled":false}`, "UTC", false}} {
		r := authenticatedRecordRequest(http.MethodPut, "/api/admin/jobs/"+id.String(), test.body, true)
		r.Header.Set("Origin", "https://admin.example.com")
		r.Header.Set("X-PgBase-CSRF", validSession().CSRFToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 204 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		items, err = service.ListJobs(t.Context())
		if err != nil || len(items) != 1 || items[0].Timezone != test.zone || items[0].Enabled != test.enabled {
			t.Fatalf("jobs=%+v err=%v", items, err)
		}
	}
}
