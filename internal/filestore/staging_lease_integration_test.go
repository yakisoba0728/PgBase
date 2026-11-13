//go:build integration

package filestore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
	"testing"
	"time"
)

func TestStagedPublicationRejectsLostOwnership(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	collectionID, recordID := uuid.New(), uuid.New()
	principal := identity.Subject{Kind: identity.Administrator, ID: uuid.New()}
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.collections(id,name,table_name,status,created_at,updated_at) VALUES($1,'staged','staged','active',now(),now())`, collectionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.record_identities(collection_id,collection_kind,scope_id,internal_id,external_id) VALUES($1,'base',$1,$2,$3)`, collectionID, recordID, recordID.String()); err != nil {
		t.Fatal(err)
	}
	eventID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO pgbase_meta.record_audit_events
		(event_id,actor_kind,actor_id,request_id,collection_id,collection_name,record_id,operation,schema_version,schema_digest,previous_version,new_version,previous_etag,new_etag,changed_fields,input_hash,idempotency_key_hash,compatibility,committed_at)
		VALUES($1,'user',$2,'file-test-request',$3,'files',$4,'create',1,decode(repeat('11',32),'hex'),NULL,1,NULL,'"etag"',ARRAY['attachment'],decode(repeat('22',32),'hex'),decode(repeat('33',32),'hex'),'none',CURRENT_TIMESTAMP)`, eventID, principal.ID, collectionID, recordID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO pgbase_meta.record_revisions(collection_id,record_id,version,validator,last_event_id,updated_at)
		VALUES($1,$2,1,decode(repeat('44',32),'hex'),$3,CURRENT_TIMESTAMP)`, collectionID, recordID, eventID)
	if err != nil {
		t.Fatal(err)
	}

	authorize := func(context.Context, pgx.Tx, identity.Subject, uuid.UUID, uuid.UUID, string, Operation) error {
		return nil
	}
	options := Options{MaximumBytes: 32, MaximumImagePixels: 1000000, ThumbnailDimension: 64, AllowedTypes: []string{"text/plain"}, TokenSecret: bytes.Repeat([]byte{7}, 32), TokenTTL: time.Minute, Policy: func(context.Context, pgx.Tx, uuid.UUID, string) (FieldPolicy, error) {
		return FieldPolicy{MaximumCount: 2}, nil
	}}
	for _, additions := range []int{1, 2} {
		t.Run(fmt.Sprintf("lost first staged lease with %d additions", additions), func(t *testing.T) {
			controller, err := NewStorageController(ctx, pool, bytes.Repeat([]byte{8}, 32), StorageConfig{Driver: "local", LocalDirectory: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			guarded, err := NewService(pool, controller, authorize, options)
			if err != nil {
				t.Fatal(err)
			}
			var staged []StagedFile
			body := []byte("lost staged lease")
			for range additions {
				item, err := guarded.Stage(ctx, principal, Upload{Filename: "lost-stage.txt", ContentType: "text/plain", Size: int64(len(body)), SHA256: sha256.Sum256(body), Body: bytes.NewReader(body)})
				if err != nil {
					t.Fatal(err)
				}
				staged = append(staged, item)
			}
			defer guarded.CompleteStaged(staged, ErrStorageConflict)
			var terminated bool
			if err := pool.QueryRow(ctx, `select pg_terminate_backend($1)`, int32(staged[0].storageLease.backend)).Scan(&terminated); err != nil || !terminated {
				t.Fatalf("terminate=%t err=%v", terminated, err)
			}
			if worked, err := guarded.DrainOne(ctx); err != nil || !worked {
				t.Fatalf("drain lost stage=%t err=%v", worked, err)
			}
			if reader, err := controller.Open(ctx, staged[0].file.ObjectKey); !errors.Is(err, ErrNotFound) {
				if err == nil {
					_ = reader.Close()
				}
				t.Fatalf("lost stage was not deleted: %v", err)
			}
			var publicationErr error
			_ = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
				_, publicationErr = guarded.Commit(ctx, tx, principal, []AttachmentChange{{CollectionID: collectionID, RecordID: recordID, Field: "attachment", Additions: staged}})
				return errStagedRollback
			})
			err = publicationErr
			if !errors.Is(err, ErrStorageConflict) {
				t.Fatalf("publication after first lease loss=%v", err)
			}
		})
	}
	t.Run("reference lock protects handoff after session loss", func(t *testing.T) {
		controller, err := NewStorageController(ctx, pool, bytes.Repeat([]byte{8}, 32), StorageConfig{Driver: "local", LocalDirectory: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		guarded, err := NewService(pool, controller, authorize, options)
		if err != nil {
			t.Fatal(err)
		}
		for {
			worked, err := guarded.DrainOne(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !worked {
				break
			}
		}
		body := []byte("handoff")
		staged, err := guarded.Stage(ctx, principal, Upload{Filename: "handoff.txt", ContentType: "text/plain", Size: int64(len(body)), SHA256: sha256.Sum256(body), Body: bytes.NewReader(body)})
		if err != nil {
			t.Fatal(err)
		}
		defer guarded.CompleteStaged([]StagedFile{staged}, nil)
		err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := guarded.Commit(ctx, tx, principal, []AttachmentChange{{CollectionID: collectionID, RecordID: recordID, Field: "attachment", Additions: []StagedFile{staged}}}); err != nil {
				return err
			}
			var terminated bool
			if err := pool.QueryRow(ctx, `select pg_terminate_backend($1)`, int32(staged.storageLease.backend)).Scan(&terminated); err != nil || !terminated {
				t.Fatalf("terminate=%t err=%v", terminated, err)
			}
			if worked, err := guarded.DrainOne(ctx); err != nil || worked {
				t.Fatalf("handoff drained=%t err=%v", worked, err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if reader, err := controller.Open(ctx, staged.file.ObjectKey); err != nil {
			t.Fatalf("committed handoff missing: %v", err)
		} else {
			_ = reader.Close()
		}
	})

}
