//go:build integration

package filestore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestUploadPublishAuthorizationDeleteAndCleanup(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}

	userID, collectionID, recordID := uuid.New(), uuid.New(), uuid.New()
	eventID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO pgbase_meta.users(id,email,password_hash,active,created_at,updated_at)
		VALUES($1,'files@example.test','fixture-hash',true,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, userID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO pgbase_meta.collections(id,name,table_name,status,created_at,updated_at)
		VALUES($1,'files','files','active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, collectionID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO pgbase_meta.fields
		(id,collection_id,name,column_name,field_type,required,is_unique,is_indexed,has_default,default_json,ordinal,created_at)
		VALUES($1,$2,'attachment','attachment','file',false,false,false,false,NULL,0,CURRENT_TIMESTAMP)`, uuid.New(), collectionID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO pgbase_meta.record_audit_events
		(event_id,actor_kind,actor_id,request_id,collection_id,collection_name,record_id,operation,schema_version,schema_digest,previous_version,new_version,previous_etag,new_etag,changed_fields,input_hash,idempotency_key_hash,compatibility,committed_at)
		VALUES($1,'user',$2,'file-test-request',$3,'files',$4,'create',1,decode(repeat('11',32),'hex'),NULL,1,NULL,'"etag"',ARRAY['attachment'],decode(repeat('22',32),'hex'),decode(repeat('33',32),'hex'),'none',CURRENT_TIMESTAMP)`, eventID, userID, collectionID, recordID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO pgbase_meta.record_revisions(collection_id,record_id,version,validator,last_event_id,updated_at)
		VALUES($1,$2,1,decode(repeat('44',32),'hex'),$3,CURRENT_TIMESTAMP)`, collectionID, recordID, eventID)
	if err != nil {
		t.Fatal(err)
	}

	objects, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	authorize := func(_ context.Context, _ pgx.Tx, principal identity.Subject, collection, record uuid.UUID, field string, operation Operation) error {
		if principal.ID != userID || collection != collectionID || record != recordID || field != "attachment" || (operation != OperationRead && operation != OperationWrite) {
			return ErrDenied
		}
		return nil
	}
	service, err := NewService(pool, objects, authorize, Options{MaximumBytes: 32, MaximumImagePixels: 1_000_000, ThumbnailDimension: 64, AllowedTypes: []string{"text/plain"}, TokenSecret: bytes.Repeat([]byte{7}, 32), TokenTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	principal := identity.Subject{Kind: identity.User, ID: userID}
	t.Run("idle storage cleanup does not queue an exclusive transition", func(t *testing.T) {
		controller, err := NewStorageController(ctx, pool, bytes.Repeat([]byte{8}, 32), StorageConfig{Driver: "local", LocalDirectory: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		_, release, err := controller.BeginStorageOperation(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		cleanupContext, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		defer cancel()
		worked, err := NewObjectMigrationService(controller).CleanupOne(cleanupContext)
		if err != nil || worked {
			t.Fatalf("idle CleanupOne while a reader is active = %t, %v", worked, err)
		}
	})
	t.Run("queued storage transition does not deadlock upload publication", func(t *testing.T) {
		controller, err := NewStorageController(ctx, pool, bytes.Repeat([]byte{8}, 32), StorageConfig{Driver: "local", LocalDirectory: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		local, err := NewLocalStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		blocked := &leaseBlockingStore{ObjectStore: local, entered: make(chan uint32, 1), release: make(chan struct{})}
		controller.mu.Lock()
		controller.store, controller.revision = blocked, 1
		controller.mu.Unlock()
		guarded, err := NewService(pool, controller, authorize, Options{MaximumBytes: 32, MaximumImagePixels: 1_000_000, ThumbnailDimension: 64, AllowedTypes: []string{"text/plain"}, TokenSecret: bytes.Repeat([]byte{7}, 32), TokenTTL: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		uploadContext, cancelUpload := context.WithCancel(ctx)
		defer cancelUpload()
		data := []byte("writer queued")
		digest := sha256.Sum256(data)
		done := make(chan error, 1)
		go func() {
			_, uploadErr := guarded.Upload(uploadContext, principal, Upload{CollectionID: collectionID, RecordID: recordID, Field: "attachment", Filename: "writer-queued.txt", ContentType: "text/plain", Size: int64(len(data)), SHA256: digest, Body: bytes.NewReader(data)})
			done <- uploadErr
		}()
		<-blocked.entered
		writer, err := pool.OpenCoordinationSession(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Close(context.Background())
		writerDone := make(chan error, 1)
		go func() {
			_, lockErr := writer.Exec(ctx, `select pg_advisory_lock($1)`, storageTransitionLock)
			writerDone <- lockErr
		}()
		deadline := time.Now().Add(time.Second)
		for {
			var waiting bool
			if err := pool.QueryRow(ctx, `select coalesce((select wait_event_type='Lock' and lower(wait_event)='advisory' from pg_stat_activity where pid=$1),false)`, writer.PgConn().PID()).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("exclusive storage transition did not queue")
			}
			time.Sleep(5 * time.Millisecond)
		}
		close(blocked.release)
		select {
		case uploadErr := <-done:
			if !errors.Is(uploadErr, ErrStorageConflict) {
				t.Fatalf("upload with a queued transition = %v, want ErrStorageConflict", uploadErr)
			}
		case <-time.After(250 * time.Millisecond):
			cancelUpload()
			uploadErr := <-done
			t.Fatalf("upload publication blocked behind its own lease: %v", uploadErr)
		}
		if err := <-writerDone; err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Exec(ctx, `select pg_advisory_unlock($1)`, storageTransitionLock); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("lost storage lease cannot publish ready metadata", func(t *testing.T) {
		controller, err := NewStorageController(ctx, pool, bytes.Repeat([]byte{8}, 32), StorageConfig{Driver: "local", LocalDirectory: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		local, err := NewLocalStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		blocked := &leaseBlockingStore{ObjectStore: local, entered: make(chan uint32, 1), release: make(chan struct{})}
		controller.mu.Lock()
		controller.store, controller.revision = blocked, 1
		controller.mu.Unlock()
		guarded, err := NewService(pool, controller, authorize, Options{MaximumBytes: 32, MaximumImagePixels: 1_000_000, ThumbnailDimension: 64, AllowedTypes: []string{"text/plain"}, TokenSecret: bytes.Repeat([]byte{7}, 32), TokenTTL: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		data := []byte("lost lease")
		digest := sha256.Sum256(data)
		done := make(chan error, 1)
		go func() {
			_, uploadErr := guarded.Upload(ctx, principal, Upload{CollectionID: collectionID, RecordID: recordID, Field: "attachment", Filename: "lost-lease.txt", ContentType: "text/plain", Size: int64(len(data)), SHA256: digest, Body: bytes.NewReader(data)})
			done <- uploadErr
		}()
		backend := <-blocked.entered
		var terminated bool
		if err := pool.QueryRow(ctx, `select pg_terminate_backend($1)`, int32(backend)).Scan(&terminated); err != nil || !terminated {
			t.Fatalf("terminate lease backend=%t err=%v", terminated, err)
		}
		if _, err := pool.Exec(ctx, `update pgbase_meta.file_storage_settings set revision=revision+1 where singleton`); err != nil {
			t.Fatal(err)
		}
		close(blocked.release)
		if err := <-done; !errors.Is(err, ErrStorageConflict) {
			t.Fatalf("upload after lease loss = %v", err)
		}
		var ready, deleted int
		var objectKey string
		if err := pool.QueryRow(ctx, `select count(*) filter(where status='ready'), count(*) filter(where status='deleted'), coalesce(max(object_key),'') from pgbase_meta.record_files where filename='lost-lease.txt'`).Scan(&ready, &deleted, &objectKey); err != nil {
			t.Fatal(err)
		}
		if ready != 0 || deleted != 1 {
			t.Fatalf("lost-lease upload ready=%d deleted=%d", ready, deleted)
		}
		if reader, err := local.Open(ctx, objectKey); !errors.Is(err, ErrNotFound) {
			if err == nil {
				_ = reader.Close()
			}
			t.Fatalf("lost-lease object was not cleaned from leased store: %v", err)
		}
	})
	unknownData := []byte("commit outcome")
	unknownHash := sha256.Sum256(unknownData)
	staged, err := service.Stage(ctx, principal, Upload{Filename: "unknown.txt", ContentType: "text/plain", Size: int64(len(unknownData)), SHA256: unknownHash, Body: bytes.NewReader(unknownData)})
	if err != nil {
		t.Fatal(err)
	}
	prepared, release, committed := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		committed <- pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := service.Commit(ctx, tx, principal, []AttachmentChange{{CollectionID: collectionID, RecordID: recordID, Field: "attachment", Additions: []StagedFile{staged}}}); err != nil {
				return err
			}
			close(prepared)
			<-release
			return nil
		})
	}()
	<-prepared
	cleaned := make(chan error, 1)
	go func() { cleaned <- service.Cleanup(ctx, []StagedFile{staged}) }()
	select {
	case err := <-cleaned:
		t.Fatalf("cleanup did not wait for committing reference: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-committed; err != nil {
		t.Fatal(err)
	}
	if err := <-cleaned; err != nil {
		t.Fatal(err)
	}
	if reader, err := objects.Open(ctx, staged.file.ObjectKey); err != nil {
		t.Fatalf("commit-unknown cleanup deleted referenced object: %v", err)
	} else {
		_ = reader.Close()
	}
	service.CompleteStaged([]StagedFile{staged}, nil)
	if err := service.Delete(ctx, principal, staged.file.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := service.DrainOne(ctx); err != nil || !worked {
		t.Fatalf("cleanup committed test file = %t, %v", worked, err)
	}

	data := []byte("private attachment")
	digest := sha256.Sum256(data)
	file, err := service.Upload(ctx, principal, Upload{CollectionID: collectionID, RecordID: recordID, Field: "attachment", Filename: "notes.txt", ContentType: "text/plain", Size: int64(len(data)), SHA256: digest, Body: bytes.NewReader(data)})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := service.List(ctx, principal, collectionID, recordID, "attachment")
	if err != nil || len(listed) != 1 || listed[0].ID != file.ID {
		t.Fatalf("List = %#v, %v", listed, err)
	}
	token, err := service.MintDownloadToken(ctx, principal, file.ID, Original)
	if err != nil {
		t.Fatal(err)
	}
	_, reader, err := service.Open(ctx, principal, file.ID, Original, token)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("Open = %q, %v", got, err)
	}
	other := identity.Subject{Kind: identity.User, ID: uuid.New()}
	if _, _, err := service.Open(ctx, other, file.ID, Original, token); !errors.Is(err, ErrDenied) {
		t.Fatalf("cross-user token Open = %v", err)
	}
	service.failUpload(ctx, file)
	var readyStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM pgbase_meta.record_files WHERE id=$1`, file.ID).Scan(&readyStatus); err != nil || readyStatus != "ready" {
		t.Fatalf("failUpload changed ready file status=%q err=%v", readyStatus, err)
	}
	if reader, err := objects.Open(ctx, file.ObjectKey); err != nil {
		t.Fatalf("failUpload deleted ready object: %v", err)
	} else {
		_ = reader.Close()
	}
	if err := service.Delete(ctx, principal, file.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Open(ctx, principal, file.ID, Original, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open deleted = %v", err)
	}
	worked, err := service.DrainOne(ctx)
	if err != nil || !worked {
		t.Fatalf("DrainOne = %t, %v", worked, err)
	}
	if _, err := objects.Open(ctx, file.ObjectKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted object Open = %v", err)
	}

	overflow := bytes.Repeat([]byte("x"), 33)
	overflowHash := sha256.Sum256(overflow)
	_, err = service.Upload(ctx, principal, Upload{CollectionID: collectionID, RecordID: recordID, Field: "attachment", Filename: "overflow.txt", ContentType: "text/plain", Size: 32, SHA256: overflowHash, Body: bytes.NewReader(overflow)})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("overflow Upload = %v, want ErrTooLarge", err)
	}
	var overflowKey, overflowStatus string
	if err := pool.QueryRow(ctx, `SELECT object_key,status FROM pgbase_meta.record_files WHERE filename='overflow.txt'`).Scan(&overflowKey, &overflowStatus); err != nil {
		t.Fatal(err)
	}
	if overflowStatus != "deleted" {
		t.Fatalf("overflow status = %q", overflowStatus)
	}
	if _, err := objects.Open(ctx, overflowKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("overflow left object: %v", err)
	}

	writes := 0
	rejectPublish := func(_ context.Context, _ pgx.Tx, current identity.Subject, collection, record uuid.UUID, field string, operation Operation) error {
		if current.ID != userID || collection != collectionID || record != recordID || field != "attachment" {
			return ErrDenied
		}
		if operation == OperationWrite {
			writes++
			if writes == 2 {
				return ErrDenied
			}
		}
		return nil
	}
	rejecting, err := NewService(pool, objects, rejectPublish, service.options)
	if err != nil {
		t.Fatal(err)
	}
	rejectedData := []byte("publish rejected")
	rejectedHash := sha256.Sum256(rejectedData)
	_, err = rejecting.Upload(ctx, principal, Upload{CollectionID: collectionID, RecordID: recordID, Field: "attachment", Filename: "rejected.txt", ContentType: "text/plain", Size: int64(len(rejectedData)), SHA256: rejectedHash, Body: bytes.NewReader(rejectedData)})
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("reauthorization Upload = %v, want ErrDenied", err)
	}
	var rejectedKey, rejectedStatus string
	if err := pool.QueryRow(ctx, `SELECT object_key,status FROM pgbase_meta.record_files WHERE filename='rejected.txt'`).Scan(&rejectedKey, &rejectedStatus); err != nil {
		t.Fatal(err)
	}
	if rejectedStatus != "deleted" {
		t.Fatalf("rejected publish status = %q", rejectedStatus)
	}
	if _, err := objects.Open(ctx, rejectedKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejected publish left object: %v", err)
	}

	fieldData := []byte("deleted with field")
	fieldHash := sha256.Sum256(fieldData)
	fieldFile, err := service.Upload(ctx, principal, Upload{CollectionID: collectionID, RecordID: recordID, Field: "attachment", Filename: "field.txt", ContentType: "text/plain", Size: int64(len(fieldData)), SHA256: fieldHash, Body: bytes.NewReader(fieldData)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM pgbase_meta.fields WHERE collection_id=$1 AND name='attachment'`, collectionID); err != nil {
		t.Fatal(err)
	}
	var metadataCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pgbase_meta.record_files WHERE id=$1`, fieldFile.ID).Scan(&metadataCount); err != nil || metadataCount != 0 {
		t.Fatalf("field deletion metadata count = %d, %v", metadataCount, err)
	}
	// Deleting the field also queues prior deleted metadata. Drain every queued
	// key; one claim has no promise to select fieldFile before those tombstones.
	for attempt := 0; attempt < 10; attempt++ {
		worked, err := service.DrainOne(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
		if attempt == 9 {
			t.Fatal("file deletion queue did not drain")
		}
	}
	if _, err := objects.Open(ctx, fieldFile.ObjectKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("field deletion left object: %v", err)
	}
}

func TestStageQueuesObjectWhenVerificationReadFails(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	readErr := errors.New("verification reader failed")
	objects := &verificationReadFailureStore{readErr: readErr}
	service, err := NewService(pool, objects, func(context.Context, pgx.Tx, identity.Subject, uuid.UUID, uuid.UUID, string, Operation) error {
		return nil
	}, Options{
		MaximumBytes: 32, MaximumImagePixels: 1_000_000, ThumbnailDimension: 64, AllowedTypes: []string{"text/plain"}, TokenSecret: bytes.Repeat([]byte{7}, 32), TokenTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("plain text")
	digest := sha256.Sum256(body)
	_, err = service.Stage(ctx, identity.Subject{Kind: identity.User, ID: uuid.New()}, Upload{Filename: "read.txt", ContentType: "text/plain", Size: int64(len(body)), SHA256: digest, Body: bytes.NewReader(body)})
	if !errors.Is(err, readErr) {
		t.Fatalf("Stage error = %v, want %v", err, readErr)
	}
	var queued string
	if err := pool.QueryRow(ctx, `select object_key from pgbase_meta.file_object_deletions`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != objects.key {
		t.Fatalf("queued key = %q, put key = %q", queued, objects.key)
	}
}

type verificationReadFailureStore struct {
	key     string
	readErr error
}

func (s *verificationReadFailureStore) Put(_ context.Context, key string, body io.Reader) error {
	s.key = key
	_, _ = io.Copy(io.Discard, body)
	return nil
}

func (s *verificationReadFailureStore) Open(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(errorReader{s.readErr}), nil
}

func (s *verificationReadFailureStore) Delete(context.Context, string) error { return nil }

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

type leaseBlockingStore struct {
	ObjectStore
	entered chan uint32
	release chan struct{}
}

func (s *leaseBlockingStore) Put(ctx context.Context, key string, body io.Reader) error {
	lease, ok := ctx.Value(storageLeaseContextKey{}).(storageLease)
	if !ok {
		return errors.New("storage lease context is missing")
	}
	s.entered <- lease.backend
	select {
	case <-s.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.ObjectStore.Put(ctx, key, body)
}

func TestAuthRecordFileUsesSharedStorageWithExplicitOwnership(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	collection, user := uuid.New(), uuid.New()
	fields := `[{"name":"avatar","type":"file","required":false,"unique":false,"indexed":false,"options":{"maxFileSize":999,"maxSelect":2,"mimeTypes":["text/plain"],"protected":false}},{"name":"secret","type":"file","required":false,"unique":false,"indexed":false,"hidden":true,"options":{"maxFileSize":32,"maxSelect":1,"mimeTypes":["text/plain"]}}]`
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.auth_collections(id,name,registration_enabled,profile_schema,fields_json,created_at,updated_at) values($1,'file_members',true,'{}',$2,now(),now())`, collection, fields); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.users(id,auth_collection_id,email,password_hash,active,created_at,updated_at) values($1,$2,'auth-file@example.test','fixture',true,now(),now())`, user, collection); err != nil {
		t.Fatal(err)
	}
	objects, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(pool, objects, func(context.Context, pgx.Tx, identity.Subject, uuid.UUID, uuid.UUID, string, Operation) error {
		return ErrDenied
	}, Options{MaximumBytes: 64, MaximumImagePixels: 1000, ThumbnailDimension: 32, AllowedTypes: []string{"text/plain"}, TokenSecret: bytes.Repeat([]byte{9}, 32), TokenTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("avatar")
	sum := sha256.Sum256(body)
	principal := identity.Subject{Kind: identity.User, ID: user}
	file, err := service.Upload(ctx, principal, Upload{CollectionID: collection, RecordID: user, Field: "avatar", Filename: "avatar.txt", ContentType: "text/plain", Size: int64(len(body)), SHA256: sum, Body: bytes.NewReader(body)})
	if err != nil {
		t.Fatal(err)
	}
	var baseCollection, authCollection *uuid.UUID
	if err := pool.QueryRow(ctx, `select collection_id,auth_collection_id from pgbase_meta.record_files where id=$1`, file.ID).Scan(&baseCollection, &authCollection); err != nil || baseCollection != nil || authCollection == nil || *authCollection != collection {
		t.Fatalf("ownership base=%v auth=%v err=%v", baseCollection, authCollection, err)
	}
	items, err := service.List(ctx, principal, collection, user, "avatar")
	if err != nil || len(items) != 1 {
		t.Fatalf("list=%#v err=%v", items, err)
	}
	if _, _, err := service.Open(ctx, identity.Subject{Kind: identity.User, ID: uuid.New()}, file.ID, Original, ""); !errors.Is(err, ErrDenied) {
		t.Fatalf("foreign open=%v", err)
	}
	publicFile, reader, err := service.OpenPublic(ctx, file.ID, Original)
	if err != nil {
		t.Fatalf("public open=%v", err)
	}
	_ = reader.Close()
	if publicFile.ID != file.ID {
		t.Fatal("wrong public file")
	}
	if _, err := service.Upload(ctx, principal, Upload{CollectionID: collection, RecordID: user, Field: "secret", Filename: "secret.txt", ContentType: "text/plain", Size: int64(len(body)), SHA256: sum, Body: bytes.NewReader(body)}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hidden owner upload=%v", err)
	}
	if _, err := service.Upload(ctx, identity.Subject{Kind: identity.Anonymous}, Upload{CollectionID: collection, RecordID: user, Field: "avatar", Filename: "anon.txt", ContentType: "text/plain", Size: int64(len(body)), SHA256: sum, Body: bytes.NewReader(body)}); !errors.Is(err, ErrDenied) {
		t.Fatalf("anonymous upload=%v", err)
	}
	if _, err := pool.Exec(ctx, `update pgbase_meta.auth_collections set update_rule='false' where id=$1`, collection); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Upload(ctx, principal, Upload{CollectionID: collection, RecordID: user, Field: "avatar", Filename: "locked.txt", ContentType: "text/plain", Size: int64(len(body)), SHA256: sum, Body: bytes.NewReader(body)}); !errors.Is(err, ErrDenied) {
		t.Fatalf("false update rule=%v", err)
	}
	large := bytes.Repeat([]byte{'x'}, 65)
	largeSum := sha256.Sum256(large)
	if _, err := service.Upload(ctx, identity.Subject{Kind: identity.Administrator, ID: uuid.New()}, Upload{CollectionID: collection, RecordID: user, Field: "avatar", Filename: "large.txt", ContentType: "text/plain", Size: 65, SHA256: largeSum, Body: bytes.NewReader(large)}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("global cap=%v", err)
	}
	if err := service.Delete(ctx, identity.Subject{Kind: identity.Administrator, ID: uuid.New()}, file.ID); err != nil {
		t.Fatal(err)
	}
}
