//go:build integration

package filestore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestResolveRecordIDUsesExactTextIdentityForFileRoutes(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	collectionID, internalID := uuid.New(), uuid.New()
	policy := `{"kind":"text","minLength":1,"maxLength":255,"pattern":"^[A-Za-z0-9가]+$"}`
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.collections(id,name,table_name,status,record_id,created_at,updated_at) VALUES($1,'file_text_ids','file_text_ids','active',$2::jsonb,now(),now())`, collectionID, policy); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.record_identities(collection_id,collection_kind,scope_id,internal_id,external_id) VALUES($1,'base',$1,$2,'ExactFile01')`, collectionID, internalID); err != nil {
		t.Fatal(err)
	}
	service := &Service{pool: pool}
	gotCollection, gotRecord, err := service.ResolveRecordID(ctx, "file_text_ids", "ExactFile01")
	if err != nil || gotCollection != collectionID || gotRecord != internalID {
		t.Fatalf("resolved=(%s,%s) err=%v", gotCollection, gotRecord, err)
	}
	if _, _, err := service.ResolveRecordID(ctx, "file_text_ids", "exactfile01"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("case-distinct file id error=%v", err)
	}
	if _, _, err := service.ResolveRecordID(ctx, "file_text_ids", internalID.String()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("internal UUID file fallback error=%v", err)
	}

	t.Run("metadata accepts a multibyte public ID at the character limit", func(t *testing.T) {
		publicID := strings.Repeat("가", 255)
		if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.record_identities(collection_id,collection_kind,scope_id,internal_id,external_id) VALUES($1,'base',$1,$2,$3)`, collectionID, uuid.New(), publicID); err != nil {
			t.Fatal(err)
		}
		service.options.MetadataCatalog = func(context.Context, pgx.Tx, uuid.UUID) (schema.Collection, error) {
			return schema.Collection{
				RecordID: schema.RecordIDPolicy{Kind: schema.RecordIDText},
				Fields:   []schema.Field{{Name: "attachment", Type: schema.TypeFile}},
			}, nil
		}
		admin := identity.Subject{Kind: identity.Administrator, ID: uuid.New()}
		metadata, err := service.ListRecordMetadata(ctx, admin, collectionID, []string{publicID})
		if err != nil || metadata[publicID]["attachment"] == nil {
			t.Fatalf("metadata did not return the public ID and its file field: %v", err)
		}
		if _, err := service.ListRecordMetadata(ctx, admin, collectionID, []string{publicID + "가"}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("oversized public ID metadata error=%v", err)
		}
	})
}
