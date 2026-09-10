package filestore

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/lifecycle"
)

type Operation string

const (
	OperationRead  Operation = "read"
	OperationWrite Operation = "write"
)

type AuthorizeFunc func(context.Context, pgx.Tx, identity.Subject, uuid.UUID, uuid.UUID, string, Operation) error

type FieldPolicy struct {
	MaximumBytes int64
	MaximumCount int
	MimeTypes    []string
	Thumbs       []string
	Required     bool
	Hidden       bool
	// Nil preserves the secure default. A policy callback must explicitly opt
	// into public delivery with a false value.
	Protected *bool
}

// OpenPublic opens an explicitly public file field. It never accepts a token
// and still resolves the file and field policy transactionally, so changing a
// field back to protected immediately revokes public URLs.
func (s *Service) OpenPublic(ctx context.Context, id uuid.UUID, variant Variant) (File, io.ReadCloser, error) {
	if id == uuid.Nil || !validVariant(variant) {
		return File{}, nil, ErrInvalid
	}
	var file File
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := scanFile(tx.QueryRow(ctx, `SELECT id,collection_id,record_id,coalesce(owner_id,'00000000-0000-0000-0000-000000000000'),field_name,filename,content_type,size_bytes,sha256,object_key,coalesce(thumbnail_object_key,''),created_at,ready_at FROM pgbase_meta.record_files WHERE id=$1 AND status='ready'`, id), &file); err != nil {
			return err
		}
		policy, err := s.policy(ctx, tx, file.CollectionID, file.Field)
		if err != nil {
			return err
		}
		if *policy.Protected || policy.Hidden {
			return ErrNotFound
		}
		if variant != Original && variant != Thumbnail && !containsVariant(policy.Thumbs, string(variant)) {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return File{}, nil, err
	}
	return s.openAuthorizedObject(ctx, file, identity.Subject{Kind: identity.Anonymous}, variant)
}

type PolicyFunc func(context.Context, pgx.Tx, uuid.UUID, string) (FieldPolicy, error)

type Options struct {
	MaximumBytes       int64
	MaximumImagePixels int64
	ThumbnailDimension int
	AllowedTypes       []string
	TokenSecret        []byte
	TokenTTL           time.Duration
	Now                func() time.Time
	Policy             PolicyFunc
	Hooks              *lifecycle.Registry
}

type Service struct {
	pool      *database.Pool
	objects   ObjectStore
	authorize AuthorizeFunc
	options   Options
	allowed   map[string]struct{}
}

func (s *Service) policy(ctx context.Context, tx pgx.Tx, collectionID uuid.UUID, field string) (FieldPolicy, error) {
	protected := true
	policy := FieldPolicy{MaximumBytes: s.options.MaximumBytes, MaximumCount: 1, MimeTypes: append([]string(nil), s.options.AllowedTypes...), Protected: &protected}
	if s.options.Policy != nil {
		resolved, err := s.options.Policy(ctx, tx, collectionID, field)
		if err != nil {
			return FieldPolicy{}, err
		}
		if resolved.MaximumBytes > 0 {
			policy.MaximumBytes = min(resolved.MaximumBytes, s.options.MaximumBytes)
		}
		if resolved.MaximumCount > 0 {
			policy.MaximumCount = resolved.MaximumCount
		}
		if len(resolved.MimeTypes) > 0 {
			policy.MimeTypes = append([]string(nil), resolved.MimeTypes...)
		}
		policy.Thumbs = append([]string(nil), resolved.Thumbs...)
		policy.Required = resolved.Required
		policy.Hidden = resolved.Hidden
		if resolved.Protected != nil {
			policy.Protected = resolved.Protected
		}
	}
	return policy, nil
}

func (s *Service) authorizeField(ctx context.Context, tx pgx.Tx, principal identity.Subject, collectionID, recordID uuid.UUID, field string, operation Operation) error {
	if err := s.authorize(ctx, tx, principal, collectionID, recordID, field, operation); err != nil {
		return err
	}
	policy, err := s.policy(ctx, tx, collectionID, field)
	if err != nil {
		return err
	}
	if policy.Hidden && principal.Kind != identity.Administrator {
		return ErrNotFound
	}
	return nil
}

func (s *Service) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if _, err := s.SweepPending(ctx, s.options.Now().Add(-15*time.Minute), 100); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		for {
			worked, err := s.DrainOne(ctx)
			if err != nil && !errors.Is(err, context.Canceled) {
				break
			}
			if !worked || err != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

type Upload struct {
	CollectionID uuid.UUID
	RecordID     uuid.UUID
	Field        string
	Filename     string
	ContentType  string
	Size         int64
	SHA256       [32]byte
	Body         io.Reader
}

type File struct {
	ID           uuid.UUID
	CollectionID uuid.UUID
	RecordID     uuid.UUID
	OwnerID      uuid.UUID
	Field        string
	Filename     string
	ContentType  string
	Size         int64
	SHA256       [32]byte
	ObjectKey    string
	ThumbnailKey string
	CreatedAt    time.Time
	ReadyAt      time.Time
	// Public is a current list-time policy observation. Downloads always
	// reauthorize; this flag is never persisted as authority.
	Public bool
}

type FileEvent struct {
	File      File
	Principal identity.Subject
	Staged    bool
}

// FileAccessEvent is the credential-free read boundary. File identity and
// principal are observations of completed authorization; changing either does
// not change the storage operation. Variant is also observational so a hook
// cannot turn an authorized request into a different rendition.
type FileAccessEvent struct {
	FileID       uuid.UUID
	CollectionID uuid.UUID
	RecordID     uuid.UUID
	Field        string
	Filename     string
	ContentType  string
	Size         int64
	Principal    identity.Subject
	Variant      Variant
}

func publicFileAccessEvent(file File, principal identity.Subject, variant Variant) *FileAccessEvent {
	return &FileAccessEvent{FileID: file.ID, CollectionID: file.CollectionID, RecordID: file.RecordID, Field: file.Field, Filename: file.Filename, ContentType: file.ContentType, Size: file.Size, Principal: principal, Variant: variant}
}

type Variant string

const (
	Original  Variant = "original"
	Thumbnail Variant = "thumbnail"
)

// OpenObject is the read-only backup seam for keys obtained from a trusted,
// transactionally consistent metadata inventory.
func (s *Service) OpenObject(ctx context.Context, key string) (io.ReadCloser, error) {
	if !storedObjectKey.MatchString(key) {
		return nil, ErrInvalid
	}
	return s.objects.Open(ctx, key)
}

func (s *Service) CollectionID(ctx context.Context, name string) (uuid.UUID, error) {
	if !fieldPattern.MatchString(name) {
		return uuid.Nil, ErrInvalid
	}
	var id uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT id FROM pgbase_meta.collections WHERE name=$1`, name).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	} else if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

func NewService(pool *database.Pool, objects ObjectStore, authorize AuthorizeFunc, options Options) (*Service, error) {
	if pool == nil || objects == nil || authorize == nil || options.MaximumBytes < 1 || options.MaximumImagePixels < 1 || options.ThumbnailDimension < 1 || len(options.TokenSecret) < 32 || options.TokenTTL <= 0 || options.TokenTTL > 15*time.Minute {
		return nil, ErrInvalid
	}
	allowed := make(map[string]struct{}, len(options.AllowedTypes))
	for _, contentType := range options.AllowedTypes {
		if contentType == "" || strings.TrimSpace(contentType) != contentType {
			return nil, ErrInvalid
		}
		allowed[contentType] = struct{}{}
	}
	if len(allowed) == 0 {
		return nil, ErrInvalid
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Service{pool: pool, objects: objects, authorize: authorize, options: options, allowed: allowed}, nil
}

var fieldPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)

func (s *Service) Upload(ctx context.Context, principal identity.Subject, input Upload) (File, error) {
	ctx, release, err := s.beginStorageOperation(ctx)
	if err != nil {
		return File{}, err
	}
	defer release()
	event := &lifecycle.Event{Kind: "file.upload", Data: &FileEvent{File: File{CollectionID: input.CollectionID, RecordID: input.RecordID, Field: input.Field, Filename: input.Filename, ContentType: input.ContentType, Size: input.Size, SHA256: input.SHA256}, Principal: principal}}
	var output File
	err = s.options.Hooks.Run(ctx, event, func() error {
		var err error
		output, err = s.upload(ctx, principal, input)
		if err == nil {
			event.Data.(*FileEvent).File = output
		}
		return err
	})
	return output, err
}

func (s *Service) upload(ctx context.Context, principal identity.Subject, input Upload) (File, error) {
	if !principal.Valid() || input.CollectionID == uuid.Nil || input.RecordID == uuid.Nil || !fieldPattern.MatchString(input.Field) || !validFilename(input.Filename) || input.Body == nil || input.Size < 0 {
		return File{}, ErrInvalid
	}
	if input.Size > s.options.MaximumBytes {
		return File{}, ErrTooLarge
	}
	if _, ok := s.allowed[input.ContentType]; !ok {
		return File{}, ErrInvalid
	}
	now := s.options.Now().UTC()
	file := File{ID: uuid.New(), CollectionID: input.CollectionID, RecordID: input.RecordID, Field: input.Field, Filename: input.Filename, ContentType: input.ContentType, Size: input.Size, SHA256: input.SHA256, CreatedAt: now}
	if principal.Kind == identity.User {
		file.OwnerID = principal.ID
	}
	file.ObjectKey = "objects/" + uuid.NewString()
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := s.authorizeField(ctx, tx, principal, input.CollectionID, input.RecordID, input.Field, OperationWrite); err != nil {
			return err
		}
		policy, err := s.policy(ctx, tx, input.CollectionID, input.Field)
		if err != nil {
			return err
		}
		if input.Size > policy.MaximumBytes {
			return ErrTooLarge
		}
		if !mimeAllowed(input.ContentType, policy.MimeTypes) {
			return ErrInvalid
		}
		// Serialize membership changes for this record field so concurrent
		// uploads cannot both observe capacity and exceed MaximumCount.
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1,0))`, input.CollectionID.String()+"/"+input.RecordID.String()+"/"+input.Field); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow(ctx, `select count(*) from pgbase_meta.record_files where collection_id=$1 and record_id=$2 and field_name=$3 and status in ('pending','ready')`, input.CollectionID, input.RecordID, input.Field).Scan(&count); err != nil {
			return err
		}
		if count >= policy.MaximumCount {
			return ErrTooMany
		}
		_, err = tx.Exec(ctx, `INSERT INTO pgbase_meta.record_files
			(id,collection_id,record_id,owner_id,field_name,filename,content_type,size_bytes,sha256,object_key,status,created_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'pending',$11)`, file.ID, file.CollectionID, file.RecordID, nullableUUID(file.OwnerID), file.Field, file.Filename, file.ContentType, file.Size, file.SHA256[:], file.ObjectKey, now)
		return err
	})
	if err != nil {
		return File{}, err
	}

	hash := sha256.New()
	counted := &countingReader{reader: input.Body, maximum: s.options.MaximumBytes}
	if err := s.objects.Put(ctx, file.ObjectKey, io.TeeReader(counted, hash)); err != nil {
		s.failUpload(context.WithoutCancel(ctx), file)
		if errors.Is(counted.err, ErrTooLarge) {
			return File{}, ErrTooLarge
		}
		return File{}, err
	}
	actualHash := hash.Sum(nil)
	if counted.count != input.Size || !hmac.Equal(actualHash, input.SHA256[:]) {
		s.failUpload(context.WithoutCancel(ctx), file)
		return File{}, ErrInvalid
	}
	if err := s.validateStoredType(ctx, file); err != nil {
		s.failUpload(context.WithoutCancel(ctx), file)
		return File{}, err
	}

	thumbnail, err := s.buildThumbnail(ctx, file)
	if err != nil {
		s.failUpload(context.WithoutCancel(ctx), file)
		return File{}, err
	}
	if len(thumbnail) != 0 {
		file.ThumbnailKey = "thumbnails/" + uuid.NewString()
		if err := s.objects.Put(ctx, file.ThumbnailKey, bytes.NewReader(thumbnail)); err != nil {
			s.failUpload(context.WithoutCancel(ctx), file)
			return File{}, err
		}
	}
	file.ReadyAt = s.options.Now().UTC()
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := s.guardStoragePublication(ctx, tx); err != nil {
			return err
		}
		if err := s.authorizeField(ctx, tx, principal, file.CollectionID, file.RecordID, file.Field, OperationWrite); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE pgbase_meta.record_files SET status='ready',thumbnail_object_key=$2,ready_at=$3
			WHERE id=$1 AND status='pending'`, file.ID, nullableString(file.ThumbnailKey), file.ReadyAt)
		if err == nil && tag.RowsAffected() != 1 {
			return ErrNotFound
		}
		return err
	})
	if err != nil {
		s.failUpload(context.WithoutCancel(ctx), file)
		return File{}, err
	}
	return file, nil
}

func mimeAllowed(value string, allowed []string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, candidate := range allowed {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == value || strings.HasSuffix(candidate, "/*") && strings.HasPrefix(value, strings.TrimSuffix(candidate, "*")) {
			return true
		}
	}
	return false
}

func (s *Service) Open(ctx context.Context, principal identity.Subject, id uuid.UUID, variant Variant, token string) (File, io.ReadCloser, error) {
	if !principal.Valid() || id == uuid.Nil || !validVariant(variant) {
		return File{}, nil, ErrInvalid
	}
	if token != "" && !s.verifyToken(token, principal, id, variant, s.options.Now()) {
		return File{}, nil, ErrDenied
	}
	var file File
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := scanFile(tx.QueryRow(ctx, `SELECT id,collection_id,record_id,coalesce(owner_id,'00000000-0000-0000-0000-000000000000'),field_name,filename,content_type,size_bytes,sha256,object_key,coalesce(thumbnail_object_key,''),created_at,ready_at
			FROM pgbase_meta.record_files WHERE id=$1 AND status='ready'`, id), &file); err != nil {
			return err
		}
		if err := s.authorizeField(ctx, tx, principal, file.CollectionID, file.RecordID, file.Field, OperationRead); err != nil {
			return err
		}
		policy, err := s.policy(ctx, tx, file.CollectionID, file.Field)
		if err != nil {
			return err
		}
		if *policy.Protected && token == "" {
			return ErrDenied
		}
		if variant != Original && variant != Thumbnail {
			if !containsVariant(policy.Thumbs, string(variant)) {
				return ErrNotFound
			}
		}
		return nil
	})
	if err != nil {
		return File{}, nil, err
	}
	return s.openAuthorizedObject(ctx, file, principal, variant)
}

func (s *Service) openAuthorizedObject(ctx context.Context, file File, principal identity.Subject, variant Variant) (File, io.ReadCloser, error) {
	key := file.ObjectKey
	var err error
	if variant == Thumbnail {
		key = file.ThumbnailKey
		if key == "" {
			return File{}, nil, ErrNotFound
		}
	}
	if variant != Original && variant != Thumbnail {
		err = s.options.Hooks.Run(ctx, &lifecycle.Event{Kind: "file.transform", Data: publicFileAccessEvent(file, principal, variant)}, func() error {
			key, err = s.variantObject(ctx, file, string(variant))
			return err
		})
		if err != nil {
			return File{}, nil, err
		}
	}
	finish, err := s.options.Hooks.Begin(ctx, &lifecycle.Event{Kind: "file.download", Data: publicFileAccessEvent(file, principal, variant)})
	if err != nil {
		finish(err)
		return File{}, nil, err
	}
	reader, err := s.objects.Open(ctx, key)
	if err != nil {
		finish(err)
		return File{}, nil, err
	}
	return file, newLifecycleReadCloser(ctx, reader, finish), nil
}

func (s *Service) Delete(ctx context.Context, principal identity.Subject, id uuid.UUID) error {
	if !principal.Valid() || id == uuid.Nil {
		return ErrInvalid
	}
	var preview File
	if err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := scanFile(tx.QueryRow(ctx, `select id,collection_id,record_id,coalesce(owner_id,'00000000-0000-0000-0000-000000000000'),field_name,filename,content_type,size_bytes,sha256,object_key,coalesce(thumbnail_object_key,''),created_at,ready_at from pgbase_meta.record_files where id=$1 and status='ready'`, id), &preview); err != nil {
			return err
		}
		return s.authorizeField(ctx, tx, principal, preview.CollectionID, preview.RecordID, preview.Field, OperationWrite)
	}); err != nil {
		return err
	}
	event := &lifecycle.Event{Kind: "file.delete", Data: &FileEvent{File: preview, Principal: principal}}
	return s.options.Hooks.Run(ctx, event, func() error {
		return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			var file File
			if err := scanFile(tx.QueryRow(ctx, `SELECT id,collection_id,record_id,coalesce(owner_id,'00000000-0000-0000-0000-000000000000'),field_name,filename,content_type,size_bytes,sha256,object_key,coalesce(thumbnail_object_key,''),created_at,ready_at
			FROM pgbase_meta.record_files WHERE id=$1 AND status='ready' FOR UPDATE`, id), &file); err != nil {
				return err
			}
			event.Data.(*FileEvent).File = file
			if err := s.authorizeField(ctx, tx, principal, file.CollectionID, file.RecordID, file.Field, OperationWrite); err != nil {
				return err
			}
			policy, err := s.policy(ctx, tx, file.CollectionID, file.Field)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1,0))`, file.CollectionID.String()+"/"+file.RecordID.String()+"/"+file.Field); err != nil {
				return err
			}
			if policy.Required {
				var count int
				if err := tx.QueryRow(ctx, `select count(*) from pgbase_meta.record_files where collection_id=$1 and record_id=$2 and field_name=$3 and status='ready'`, file.CollectionID, file.RecordID, file.Field).Scan(&count); err != nil {
					return err
				}
				if count <= 1 {
					return ErrRequired
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE pgbase_meta.record_files SET status='deleted',deleted_at=CURRENT_TIMESTAMP WHERE id=$1`, id); err != nil {
				return err
			}
			return enqueueFileObjects(ctx, tx, file)
		})
	})
}

func (s *Service) List(ctx context.Context, principal identity.Subject, collectionID, recordID uuid.UUID, field string) ([]File, error) {
	if !principal.Valid() || collectionID == uuid.Nil || recordID == uuid.Nil || !fieldPattern.MatchString(field) {
		return nil, ErrInvalid
	}
	files := make([]File, 0)
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := s.authorizeField(ctx, tx, principal, collectionID, recordID, field, OperationRead); err != nil {
			return err
		}
		policy, err := s.policy(ctx, tx, collectionID, field)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id,collection_id,record_id,coalesce(owner_id,'00000000-0000-0000-0000-000000000000'),field_name,filename,content_type,size_bytes,sha256,object_key,coalesce(thumbnail_object_key,''),created_at,ready_at
			FROM pgbase_meta.record_files WHERE collection_id=$1 AND record_id=$2 AND field_name=$3 AND status='ready' ORDER BY created_at,id`, collectionID, recordID, field)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var file File
			if err := scanFile(rows, &file); err != nil {
				return err
			}
			file.Public = !*policy.Protected && !policy.Hidden
			files = append(files, file)
		}
		return rows.Err()
	})
	return files, err
}

// SweepPending turns abandoned uploads into durable deletion work. The row
// lock makes a racing publisher observe deleted rather than resurrecting it.
func (s *Service) SweepPending(ctx context.Context, olderThan time.Time, limit int) (int, error) {
	if limit < 1 || limit > 1000 {
		return 0, ErrInvalid
	}
	queued := 0
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id,object_key,coalesce(thumbnail_object_key,'')
			FROM pgbase_meta.record_files WHERE status='pending' AND created_at<$1
			ORDER BY created_at,id LIMIT $2 FOR UPDATE SKIP LOCKED`, olderThan.UTC(), limit)
		if err != nil {
			return err
		}
		type abandoned struct {
			id                uuid.UUID
			object, thumbnail string
		}
		items := make([]abandoned, 0, limit)
		for rows.Next() {
			var item abandoned
			if err := rows.Scan(&item.id, &item.object, &item.thumbnail); err != nil {
				rows.Close()
				return err
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, item := range items {
			if _, err := tx.Exec(ctx, `UPDATE pgbase_meta.record_files SET status='deleted',deleted_at=CURRENT_TIMESTAMP WHERE id=$1 AND status='pending'`, item.id); err != nil {
				return err
			}
			if err := enqueueObjects(ctx, tx, item.object, item.thumbnail); err != nil {
				return err
			}
			queued++
		}
		return nil
	})
	return queued, err
}

func (s *Service) DrainOne(ctx context.Context) (bool, error) {
	worked := false
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock_shared($1)`, ObjectGCKey); err != nil {
			return err
		}
		var key string
		err := tx.QueryRow(ctx, `SELECT object_key FROM pgbase_meta.file_object_deletions ORDER BY queued_at,object_key LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&key)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := s.objects.Delete(ctx, key); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM pgbase_meta.file_object_deletions WHERE object_key=$1`, key); err != nil {
			return err
		}
		worked = true
		return nil
	})
	return worked, err
}

func (s *Service) MintDownloadToken(ctx context.Context, principal identity.Subject, id uuid.UUID, variant Variant) (string, error) {
	if !principal.Valid() || id == uuid.Nil || !validVariant(variant) {
		return "", ErrInvalid
	}
	var file File
	if err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := scanFile(tx.QueryRow(ctx, `SELECT id,collection_id,record_id,coalesce(owner_id,'00000000-0000-0000-0000-000000000000'),field_name,filename,content_type,size_bytes,sha256,object_key,coalesce(thumbnail_object_key,''),created_at,ready_at
			FROM pgbase_meta.record_files WHERE id=$1 AND status='ready'`, id), &file); err != nil {
			return err
		}
		if variant == Thumbnail && file.ThumbnailKey == "" {
			return ErrNotFound
		}
		if variant != Original && variant != Thumbnail {
			policy, err := s.policy(ctx, tx, file.CollectionID, file.Field)
			if err != nil {
				return err
			}
			if !containsVariant(policy.Thumbs, string(variant)) {
				return ErrNotFound
			}
		}
		return s.authorizeField(ctx, tx, principal, file.CollectionID, file.RecordID, file.Field, OperationRead)
	}); err != nil {
		return "", err
	}
	var token string
	err := s.options.Hooks.Run(ctx, &lifecycle.Event{Kind: "file.token", Data: publicFileAccessEvent(file, principal, variant)}, func() error {
		token = s.signDownloadToken(principal, id, variant)
		return nil
	})
	return token, err
}

func (s *Service) signDownloadToken(principal identity.Subject, id uuid.UUID, variant Variant) string {
	payload := make([]byte, 0, 16+16+1+8)
	payload = append(payload, principal.ID[:]...)
	payload = append(payload, id[:]...)
	payload = append(payload, byte(len(variant)))
	payload = append(payload, variant...)
	expiry := make([]byte, 8)
	binary.BigEndian.PutUint64(expiry, uint64(s.options.Now().Add(s.options.TokenTTL).Unix()))
	payload = append(payload, expiry...)
	mac := hmac.New(sha256.New, s.options.TokenSecret)
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(append(payload, mac.Sum(nil)...))
}

func (s *Service) verifyToken(token string, principal identity.Subject, id uuid.UUID, variant Variant, now time.Time) bool {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	minimum := 16 + 16 + 1 + 8 + sha256.Size
	if err != nil || len(raw) < minimum {
		return false
	}
	payload, supplied := raw[:len(raw)-sha256.Size], raw[len(raw)-sha256.Size:]
	mac := hmac.New(sha256.New, s.options.TokenSecret)
	_, _ = mac.Write(payload)
	if !hmac.Equal(supplied, mac.Sum(nil)) || !hmac.Equal(payload[:16], principal.ID[:]) || !hmac.Equal(payload[16:32], id[:]) {
		return false
	}
	length := int(payload[32])
	if len(payload) != 33+length+8 || string(payload[33:33+length]) != string(variant) {
		return false
	}
	expires := int64(binary.BigEndian.Uint64(payload[len(payload)-8:]))
	return expires >= now.Unix()
}

func (s *Service) failUpload(ctx context.Context, file File) {
	_ = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `UPDATE pgbase_meta.record_files SET status='deleted',deleted_at=CURRENT_TIMESTAMP WHERE id=$1 AND status='pending' RETURNING id`, file.ID).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}
		return enqueueObjects(ctx, tx, file.ObjectKey, file.ThumbnailKey)
	})
	for {
		worked, err := s.DrainOne(ctx)
		if err != nil || !worked {
			return
		}
	}
}

func enqueueObjects(ctx context.Context, tx pgx.Tx, keys ...string) error {
	for _, key := range keys {
		if key == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO pgbase_meta.file_object_deletions(object_key,queued_at) VALUES($1,CURRENT_TIMESTAMP) ON CONFLICT DO NOTHING`, key); err != nil {
			return err
		}
	}
	return nil
}

func enqueueFileObjects(ctx context.Context, tx pgx.Tx, file File) error {
	if err := enqueueObjects(ctx, tx, file.ObjectKey, file.ThumbnailKey); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `delete from pgbase_meta.record_file_variants where file_id=$1 returning object_key`, file.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return err
		}
		if err := enqueueObjects(ctx, tx, key); err != nil {
			return err
		}
	}
	return rows.Err()
}

func scanFile(row pgx.Row, file *File) error {
	var hash []byte
	if err := row.Scan(&file.ID, &file.CollectionID, &file.RecordID, &file.OwnerID, &file.Field, &file.Filename, &file.ContentType, &file.Size, &hash, &file.ObjectKey, &file.ThumbnailKey, &file.CreatedAt, &file.ReadyAt); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if len(hash) != sha256.Size {
		return ErrInvalid
	}
	copy(file.SHA256[:], hash)
	return nil
}

func validFilename(name string) bool {
	if name == "" || len(name) > 255 || !utf8.ValidString(name) || filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
		return false
	}
	for _, character := range name {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableUUID(value uuid.UUID) any {
	if value == uuid.Nil {
		return nil
	}
	return value
}

type countingReader struct {
	reader  io.Reader
	maximum int64
	count   int64
	err     error
}

func (r *countingReader) Read(buffer []byte) (int, error) {
	if r.count >= r.maximum+1 {
		r.err = ErrTooLarge
		return 0, r.err
	}
	remaining := r.maximum + 1 - r.count
	if int64(len(buffer)) > remaining {
		buffer = buffer[:remaining]
	}
	n, err := r.reader.Read(buffer)
	r.count += int64(n)
	if r.count > r.maximum {
		r.err = ErrTooLarge
		return n, r.err
	}
	return n, err
}
