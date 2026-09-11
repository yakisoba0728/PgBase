package filestore

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/lifecycle"
)

var errStagedRollback = errors.New("staged upload rolled back")

// StagedFile is an immutable handle to bytes uploaded before a record
// transaction. Its fields are intentionally private so callers cannot swap an
// object key, digest, or declared metadata between Stage and Commit.
type StagedFile struct {
	file           File
	finish         func(error)
	storageRelease func()
	storageLease   storageLease
}

func (s StagedFile) ID() uuid.UUID       { return s.file.ID }
func (s StagedFile) Filename() string    { return s.file.Filename }
func (s StagedFile) ContentType() string { return s.file.ContentType }
func (s StagedFile) Size() int64         { return s.file.Size }
func (s StagedFile) SHA256() [32]byte    { return s.file.SHA256 }

type AttachmentChange struct {
	CollectionID uuid.UUID
	RecordID     uuid.UUID
	Field        string
	Additions    []StagedFile
	DeleteIDs    []uuid.UUID
}

// Stage durably writes and validates bytes without assuming that the owning
// record exists yet. Call Commit in the record transaction or Cleanup on every
// failure path.
func (s *Service) Stage(ctx context.Context, principal identity.Subject, input Upload) (StagedFile, error) {
	ctx, release, err := s.beginStorageOperation(ctx)
	if err != nil {
		return StagedFile{}, err
	}
	event := &lifecycle.Event{Kind: "file.upload", Data: &FileEvent{File: File{Filename: input.Filename, ContentType: input.ContentType, Size: input.Size, SHA256: input.SHA256}, Principal: principal, Staged: true}}
	finish, err := s.options.Hooks.Begin(ctx, event)
	if err != nil {
		release()
		finish(err)
		return StagedFile{}, err
	}
	staged, err := s.stage(ctx, principal, input)
	if err != nil {
		release()
		finish(err)
		return StagedFile{}, err
	}
	event.Data.(*FileEvent).File = staged.file
	staged.finish = finish
	staged.storageRelease = release
	staged.storageLease, _ = ctx.Value(storageLeaseContextKey{}).(storageLease)
	return staged, nil
}

func (s *Service) stage(ctx context.Context, principal identity.Subject, input Upload) (StagedFile, error) {
	if !principal.Valid() || !validFilename(input.Filename) || input.Body == nil || input.Size < 0 || input.Size > s.options.MaximumBytes {
		return StagedFile{}, ErrInvalid
	}
	if _, ok := s.allowed[input.ContentType]; !ok {
		return StagedFile{}, ErrInvalid
	}
	file := File{ID: uuid.New(), OwnerID: principal.ID, Filename: input.Filename, ContentType: input.ContentType, Size: input.Size, SHA256: input.SHA256, ObjectKey: "objects/" + uuid.NewString(), CreatedAt: s.options.Now().UTC()}
	counted, hash := &countingReader{reader: input.Body, maximum: s.options.MaximumBytes}, sha256.New()
	if err := s.objects.Put(ctx, file.ObjectKey, io.TeeReader(counted, hash)); err != nil {
		_ = s.queueUnreferenced(context.WithoutCancel(ctx), file.ObjectKey)
		if errors.Is(counted.err, ErrTooLarge) {
			return StagedFile{}, ErrTooLarge
		}
		return StagedFile{}, err
	}
	if counted.count != input.Size || !hmac.Equal(hash.Sum(nil), input.SHA256[:]) {
		_ = s.queueUnreferenced(context.WithoutCancel(ctx), file.ObjectKey)
		return StagedFile{}, ErrInvalid
	}
	reader, err := s.objects.Open(ctx, file.ObjectKey)
	if err != nil {
		_ = s.queueUnreferenced(context.WithoutCancel(ctx), file.ObjectKey)
		return StagedFile{}, err
	}
	buffer := make([]byte, 512)
	n, readErr := io.ReadFull(reader, buffer)
	_ = reader.Close()
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
		_ = s.queueUnreferenced(context.WithoutCancel(ctx), file.ObjectKey)
		return StagedFile{}, readErr
	}
	detected := strings.TrimSpace(strings.Split(http.DetectContentType(buffer[:n]), ";")[0])
	if detected != input.ContentType {
		_ = s.queueUnreferenced(context.WithoutCancel(ctx), file.ObjectKey)
		return StagedFile{}, ErrInvalid
	}
	return StagedFile{file: file}, nil
}

// Commit attaches staged objects in the caller's transaction. Repeating the
// exact same staged handles is safe and returns the already-ready rows.
func (s *Service) Commit(ctx context.Context, tx pgx.Tx, principal identity.Subject, changes []AttachmentChange) ([]File, error) {
	if tx == nil || !principal.Valid() {
		return nil, ErrInvalid
	}
	publicationCtx := ctx
	hasAddition := false
	for _, change := range changes {
		for _, staged := range change.Additions {
			hasAddition = true
			if staged.storageLease.store == nil {
				continue
			}
			if existing, ok := publicationCtx.Value(storageLeaseContextKey{}).(storageLease); ok && existing.revision != staged.storageLease.revision {
				return nil, ErrStorageConflict
			}
			publicationCtx = context.WithValue(publicationCtx, storageLeaseContextKey{}, staged.storageLease)
		}
	}
	if hasAddition {
		if err := s.guardStoragePublication(publicationCtx, tx); err != nil {
			return nil, err
		}
	}
	result := make([]File, 0)
	for _, change := range changes {
		if change.CollectionID == uuid.Nil || change.RecordID == uuid.Nil || !fieldPattern.MatchString(change.Field) {
			return nil, ErrInvalid
		}
		if err := s.authorizeField(ctx, tx, principal, change.CollectionID, change.RecordID, change.Field, OperationWrite); err != nil {
			return nil, err
		}
		policy, err := s.policy(ctx, tx, change.CollectionID, change.Field)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1,0))`, change.CollectionID.String()+"/"+change.RecordID.String()+"/"+change.Field); err != nil {
			return nil, err
		}
		for _, id := range change.DeleteIDs {
			var object, thumbnail string
			err := tx.QueryRow(ctx, `update pgbase_meta.record_files set status='deleted',deleted_at=current_timestamp where id=$1 and collection_id=$2 and record_id=$3 and field_name=$4 and status='ready' returning object_key,coalesce(thumbnail_object_key,'')`, id, change.CollectionID, change.RecordID, change.Field).Scan(&object, &thumbnail)
			if errors.Is(err, pgx.ErrNoRows) {
				var status string
				if lookupErr := tx.QueryRow(ctx, `select status from pgbase_meta.record_files where id=$1 and collection_id=$2 and record_id=$3 and field_name=$4`, id, change.CollectionID, change.RecordID, change.Field).Scan(&status); lookupErr != nil || status != "deleted" {
					return nil, ErrNotFound
				}
				continue
			}
			if err != nil {
				return nil, err
			}
			if err := enqueueFileObjects(ctx, tx, File{ID: id, ObjectKey: object, ThumbnailKey: thumbnail}); err != nil {
				return nil, err
			}
		}
		var count int
		if err := tx.QueryRow(ctx, `select count(*) from pgbase_meta.record_files where collection_id=$1 and record_id=$2 and field_name=$3 and status='ready'`, change.CollectionID, change.RecordID, change.Field).Scan(&count); err != nil {
			return nil, err
		}
		newCount := 0
		for _, staged := range change.Additions {
			if err := lockObjectReference(ctx, tx, staged.file.ObjectKey); err != nil {
				return nil, err
			}
			var object, status string
			err := tx.QueryRow(ctx, `select object_key,status from pgbase_meta.record_files where id=$1`, staged.file.ID).Scan(&object, &status)
			if errors.Is(err, pgx.ErrNoRows) {
				newCount++
				continue
			}
			if err != nil || object != staged.file.ObjectKey || status != "ready" {
				return nil, ErrInvalid
			}
		}
		if count+newCount > policy.MaximumCount {
			return nil, ErrTooMany
		}
		if policy.Required && count+newCount == 0 {
			return nil, ErrRequired
		}
		for _, staged := range change.Additions {
			file := staged.file
			if file.ID == uuid.Nil || file.ObjectKey == "" || file.Size > policy.MaximumBytes {
				return nil, ErrTooLarge
			}
			if !mimeAllowed(file.ContentType, policy.MimeTypes) {
				return nil, ErrInvalid
			}
			file.CollectionID, file.RecordID, file.Field, file.ReadyAt = change.CollectionID, change.RecordID, change.Field, s.options.Now().UTC()
			if principal.Kind != identity.User {
				file.OwnerID = uuid.Nil
			}
			tag, err := tx.Exec(ctx, `insert into pgbase_meta.record_files(id,collection_id,record_id,owner_id,field_name,filename,content_type,size_bytes,sha256,object_key,status,created_at,ready_at) values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'ready',$11,$12) on conflict(id) do nothing`, file.ID, file.CollectionID, file.RecordID, nullableUUID(file.OwnerID), file.Field, file.Filename, file.ContentType, file.Size, file.SHA256[:], file.ObjectKey, file.CreatedAt, file.ReadyAt)
			if err != nil {
				return nil, err
			}
			if tag.RowsAffected() == 0 {
				var existing File
				if err := scanFile(tx.QueryRow(ctx, `select id,coalesce(collection_id,auth_collection_id),coalesce(record_id,auth_user_id),coalesce(owner_id,'00000000-0000-0000-0000-000000000000'),field_name,filename,content_type,size_bytes,sha256,object_key,coalesce(thumbnail_object_key,''),created_at,ready_at from pgbase_meta.record_files where id=$1 and status='ready'`, file.ID), &existing); err != nil {
					return nil, err
				}
				if existing.ObjectKey != file.ObjectKey || existing.CollectionID != file.CollectionID || existing.RecordID != file.RecordID || existing.Field != file.Field {
					return nil, ErrInvalid
				}
				file = existing
			}
			result = append(result, file)
		}
	}
	return result, nil
}

// Cleanup only queues objects that have no metadata reference. The worker
// performs physical deletion behind the backup/GC gate.
func (s *Service) Cleanup(ctx context.Context, staged []StagedFile) error {
	var cleanupErr error
	for _, item := range staged {
		if err := s.queueUnreferenced(ctx, item.file.ObjectKey); err != nil {
			cleanupErr = err
			break
		}
	}
	if cleanupErr == nil {
		cleanupErr = errStagedRollback
	}
	s.CompleteStaged(staged, cleanupErr)
	if errors.Is(cleanupErr, errStagedRollback) {
		return nil
	}
	return cleanupErr
}

// CompleteStaged finishes upload hooks after the owner knows whether the
// transaction committed. Repeated calls are safe because lifecycle finishers
// run once.
func (s *Service) CompleteStaged(staged []StagedFile, operationErr error) {
	for _, item := range staged {
		if item.finish != nil {
			item.finish(operationErr)
		}
		if item.storageRelease != nil {
			item.storageRelease()
		}
	}
}

func (s *Service) queueUnreferenced(ctx context.Context, key string) error {
	return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockObjectReference(ctx, tx, key); err != nil {
			return err
		}
		var referenced bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from pgbase_meta.record_files where object_key=$1)`, key).Scan(&referenced); err != nil {
			return err
		}
		if referenced {
			return nil
		}
		return enqueueObjects(ctx, tx, key)
	})
}

func lockObjectReference(ctx context.Context, tx pgx.Tx, key string) error {
	_, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1,0))`, "file-object/"+key)
	return err
}
