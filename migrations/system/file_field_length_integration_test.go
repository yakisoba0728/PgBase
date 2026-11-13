//go:build integration

package system_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/testutil"
	"github.com/yakisoba0728/PgBase/migrations/system"
)

func TestFileFieldLengthUpgradeFrom66PreservesExistingRows(t *testing.T) {
	ctx := t.Context()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	migrations := system.Migrations()
	for _, m := range migrations {
		if m.Version <= 66 {
			if _, err := pool.Exec(ctx, m.SQL); err != nil {
				t.Fatalf("migration %d: %v", m.Version, err)
			}
		}
	}
	collection, user := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.auth_collections(id,name,created_at,updated_at) values($1,'long_file_fields',now(),now())`, collection); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.users(id,auth_collection_id,email,password_hash,active,created_at,updated_at) values($1,$2,'migration@example.test','fixture',true,now(),now())`, user, collection); err != nil {
		t.Fatal(err)
	}
	insert := func(field string) error {
		_, err := pool.Exec(ctx, `insert into pgbase_meta.record_files(id,auth_collection_id,auth_user_id,field_name,filename,content_type,size_bytes,sha256,object_key,status,created_at,ready_at) values($1,$2,$3,$4,'file.txt','text/plain',0,decode(repeat('00',32),'hex'),$5,'ready',now(),now())`, uuid.New(), collection, user, field, "objects/"+uuid.NewString())
		return err
	}
	if err := insert("a" + strings.Repeat("x", 47)); err != nil {
		t.Fatal(err)
	}
	if err := insert("a" + strings.Repeat("x", 63)); err == nil {
		t.Fatal("66 accepted64 before upgrade")
	}
	var applied bool
	for _, m := range migrations {
		if m.Version == 67 {
			if _, err := pool.Exec(ctx, m.SQL); err != nil {
				t.Fatal(err)
			}
			applied = true
		}
	}
	if !applied {
		t.Fatal("67 not registered")
	}
	if err := insert("b" + strings.Repeat("x", 47)); err != nil {
		t.Fatal(err)
	}
	if err := insert("a" + strings.Repeat("x", 63)); err != nil {
		t.Fatal(err)
	}
	if err := insert("a" + strings.Repeat("x", 64)); err == nil {
		t.Fatal("65 accepted")
	}
	var count int
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.record_files`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}
