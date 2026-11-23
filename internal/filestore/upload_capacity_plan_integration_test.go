//go:build integration

package filestore

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

// The upload capacity check runs under the record field's advisory lock on
// every upload, so it must stay an index lookup as record_files grows.
func TestUploadCapacityCountUsesRecordFieldIndexes(t *testing.T) {
	ctx := t.Context()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	collection, record := uuid.New(), uuid.New()
	authCollection, user := uuid.New(), uuid.New()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The plan depends only on the rows and statistics. Replica mode skips the
	// foreign-key triggers that would otherwise need full record and user
	// fixtures for tens of thousands of rows.
	if err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `set local session_replication_role = replica`); err != nil {
			return err
		}
		const base = `insert into pgbase_meta.record_files(id,collection_id,record_id,field_name,filename,content_type,size_bytes,sha256,object_key,status,created_at,ready_at,deleted_at)
			select gen_random_uuid(),$1,$2,'files','f.bin','application/octet-stream',1,decode(repeat('aa',32),'hex'),'objects/'||gen_random_uuid(),$3,now(),
				case when $3 in ('ready','deleted') then now() end, case when $3='deleted' then now() end
			from generate_series(1,$4)`
		const auth = `insert into pgbase_meta.record_files(id,auth_collection_id,auth_user_id,field_name,filename,content_type,size_bytes,sha256,object_key,status,created_at,ready_at,deleted_at)
			select gen_random_uuid(),$1,$2,'files','f.bin','application/octet-stream',1,decode(repeat('aa',32),'hex'),'objects/'||gen_random_uuid(),$3,now(),
				case when $3 in ('ready','deleted') then now() end, case when $3='deleted' then now() end
			from generate_series(1,$4)`
		// Unrelated history: many records' ready and deleted files plus a few
		// in-flight uploads.
		if _, err := tx.Exec(ctx, `insert into pgbase_meta.record_files(id,collection_id,record_id,field_name,filename,content_type,size_bytes,sha256,object_key,status,created_at,ready_at,deleted_at)
			select gen_random_uuid(),$1,gen_random_uuid(),'files','f.bin','application/octet-stream',1,decode(repeat('aa',32),'hex'),'objects/'||gen_random_uuid(),
				case when n%10=0 then 'deleted' when n%100=1 then 'pending' else 'ready' end,now(),
				case when n%100<>1 then now() end, case when n%10=0 then now() end
			from generate_series(1,40000) n`, collection); err != nil {
			return err
		}
		for _, fixture := range []struct {
			sql    string
			status string
			count  int
		}{
			{base, "ready", 2}, {base, "pending", 1}, {base, "deleted", 3},
			{auth, "ready", 1}, {auth, "pending", 2}, {auth, "deleted", 1},
		} {
			owner, id := collection, record
			if fixture.sql == auth {
				owner, id = authCollection, user
			}
			if _, err := tx.Exec(ctx, fixture.sql, owner, id, fixture.status, fixture.count); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		conn.Release()
		t.Fatal(err)
	}
	conn.Release()
	if _, err := pool.Exec(ctx, `analyze pgbase_meta.record_files`); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []struct {
		collection, record uuid.UUID
		want               int
	}{{collection, record, 3}, {authCollection, user, 3}} {
		var count int
		if err := pool.QueryRow(ctx, uploadCapacityCountSQL, owner.collection, owner.record, "files").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != owner.want {
			t.Fatalf("capacity count=%d, want %d pending and ready files", count, owner.want)
		}
		rows, err := pool.Query(ctx, `explain `+uploadCapacityCountSQL, owner.collection, owner.record, "files")
		if err != nil {
			t.Fatal(err)
		}
		lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
		plan := strings.Join(lines, "\n")
		if strings.Contains(plan, "Seq Scan") {
			t.Fatalf("capacity count scans record_files:\n%s", plan)
		}
		for _, index := range []string{"record_files_record_idx", "record_files_auth_record_idx", "record_files_pending_record_idx", "record_files_pending_auth_record_idx"} {
			if !strings.Contains(plan, index) {
				t.Fatalf("capacity count plan does not use %s:\n%s", index, plan)
			}
		}
		t.Logf("capacity count plan:\n%s", plan)
	}
}
