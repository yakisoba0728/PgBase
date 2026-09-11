package filestore

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type StorageMigrationStatus struct {
	ID            uuid.UUID `json:"id"`
	Status        string    `json:"status"`
	CopiedObjects int64     `json:"copiedObjects"`
	TotalObjects  int64     `json:"totalObjects"`
	Failure       string    `json:"failure,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type storageSnapshot struct {
	Driver, LocalDirectory, Bucket, Region, Endpoint, AccessKey, SecretKey string
	UsePathStyle, AllowInsecureLoopback                                    bool
}

// ObjectMigrationService durably copies every referenced object before an
// atomic storage-generation cutover. Its state is safe to resume after a
// process exit; only one runner per migration claims ledger rows at a time.
type ObjectMigrationService struct {
	controller *StorageController
}

func NewObjectMigrationService(controller *StorageController) *ObjectMigrationService {
	return &ObjectMigrationService{controller: controller}
}

func (s *ObjectMigrationService) Preview(ctx context.Context, destination StorageUpdate) (StorageMigrationStatus, error) {
	current, _, err := s.controller.load(ctx)
	if err != nil {
		return StorageMigrationStatus{}, err
	}
	destination.Revision = current.Revision
	_, _, err = s.snapshots(ctx, destination)
	if err != nil {
		return StorageMigrationStatus{}, err
	}
	var count int64
	err = s.controller.pool.QueryRow(ctx, referencedObjectCountSQL).Scan(&count)
	return StorageMigrationStatus{Status: "preview", TotalObjects: count}, err
}

func (s *ObjectMigrationService) Start(ctx context.Context, id uuid.UUID, destination StorageUpdate) (StorageMigrationStatus, error) {
	if id == uuid.Nil {
		return StorageMigrationStatus{}, ErrInvalid
	}
	requestJSON, err := json.Marshal(destination)
	if err != nil {
		return StorageMigrationStatus{}, err
	}
	requestHash := sha256.Sum256(requestJSON)
	var result StorageMigrationStatus
	err = s.controller.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, storageTransitionLock); err != nil {
			return err
		}
		var existingHash []byte
		existingErr := tx.QueryRow(ctx, `select request_hash,id,status,copied_objects,total_objects,failure,created_at,updated_at from pgbase_meta.file_storage_migrations where id=$1`, id).Scan(&existingHash, &result.ID, &result.Status, &result.CopiedObjects, &result.TotalObjects, &result.Failure, &result.CreatedAt, &result.UpdatedAt)
		if existingErr == nil {
			if !equalBytes(existingHash, requestHash[:]) {
				return ErrStorageConflict
			}
			return nil
		}
		if !errors.Is(existingErr, pgx.ErrNoRows) {
			return existingErr
		}
		var active bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from pgbase_meta.file_storage_migrations where status in ('copying','canceling','ready','activating'))`).Scan(&active); err != nil {
			return err
		}
		if active {
			return ErrStorageConflict
		}
		current, _, err := s.controller.load(ctx)
		if err != nil {
			return err
		}
		destination.Revision = current.Revision
		sourceBlob, destinationBlob, err := s.snapshots(ctx, destination)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `insert into pgbase_meta.file_storage_migrations(id,status,base_revision,source_config_ciphertext,destination_config_ciphertext,request_hash) values($1,'copying',$2,$3,$4,$5)`, id, current.Revision, sourceBlob, destinationBlob, requestHash[:]); err != nil {
			return err
		}
		if err = insertMigrationObjects(ctx, tx, id); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `update pgbase_meta.file_storage_migrations set total_objects=(select count(*) from pgbase_meta.file_storage_migration_objects where migration_id=$1),copied_objects=(select count(*) from pgbase_meta.file_storage_migration_objects where migration_id=$1 and status='verified'),updated_at=CURRENT_TIMESTAMP where id=$1 returning id,status,copied_objects,total_objects,failure,created_at,updated_at`, id).Scan(&result.ID, &result.Status, &result.CopiedObjects, &result.TotalObjects, &result.Failure, &result.CreatedAt, &result.UpdatedAt)
	})
	if err != nil {
		return StorageMigrationStatus{}, err
	}
	return result, nil
}

func (s *ObjectMigrationService) Resume(ctx context.Context, id uuid.UUID) error {
	tag, err := s.controller.pool.Exec(ctx, `with resumed as (update pgbase_meta.file_storage_migrations set status='copying',failure='',updated_at=CURRENT_TIMESTAMP where id=$1 and status in ('copying','failed','ready') and not cancel_requested returning 1) update pgbase_meta.file_storage_migration_objects set status='pending',failure='',updated_at=CURRENT_TIMESTAMP where migration_id=$1 and status='failed' and exists(select 1 from resumed)`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		status, statusErr := s.Status(ctx, id)
		if statusErr != nil || status.Status != "copying" {
			return ErrStorageConflict
		}
	}
	return nil
}

// Run executes migrations under the application's background lifecycle. A
// canceled process leaves durable copying state for the next process to resume.
func (s *ObjectMigrationService) Run(ctx context.Context) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		var id uuid.UUID
		err := s.controller.pool.QueryRow(ctx, `select id from pgbase_meta.file_storage_migrations where status in ('copying','canceling','ready') order by created_at limit 1`).Scan(&id)
		if err == nil {
			if runErr := s.run(ctx, id); runErr != nil && ctx.Err() == nil {
				// A durable failed state was recorded; continue serving later resumes.
			}
		} else if !errors.Is(err, pgx.ErrNoRows) && ctx.Err() == nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		_, _ = s.CleanupOne(ctx)
	}
}

func (s *ObjectMigrationService) Cancel(ctx context.Context, id uuid.UUID) error {
	return s.controller.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var destination []byte
		var status string
		if err := tx.QueryRow(ctx, `select destination_config_ciphertext,status from pgbase_meta.file_storage_migrations where id=$1 for update`, id).Scan(&destination, &status); err != nil {
			return err
		}
		if status != "copying" && status != "canceling" && status != "ready" && status != "failed" {
			return ErrStorageConflict
		}
		next := "canceled"
		if status == "copying" || status == "canceling" {
			next = "canceling"
		}
		if _, err := tx.Exec(ctx, `update pgbase_meta.file_storage_migrations set cancel_requested=true,status=$2,updated_at=CURRENT_TIMESTAMP where id=$1`, id, next); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `insert into pgbase_meta.file_storage_source_cleanup(migration_id,object_key,source_config_ciphertext,not_before) select $1,object_key,$2,CURRENT_TIMESTAMP+interval '1 hour' from pgbase_meta.file_storage_migration_objects where migration_id=$1 and status='verified' and created_destination on conflict do nothing`, id, destination)
		return err
	})
}

func (s *ObjectMigrationService) Status(ctx context.Context, id uuid.UUID) (StorageMigrationStatus, error) {
	var v StorageMigrationStatus
	err := s.controller.pool.QueryRow(ctx, `select id,status,copied_objects,total_objects,failure,created_at,updated_at from pgbase_meta.file_storage_migrations where id=$1`, id).Scan(&v.ID, &v.Status, &v.CopiedObjects, &v.TotalObjects, &v.Failure, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return v, err
}

func (s *ObjectMigrationService) Latest(ctx context.Context) (StorageMigrationStatus, error) {
	var v StorageMigrationStatus
	err := s.controller.pool.QueryRow(ctx, `select id,status,copied_objects,total_objects,failure,created_at,updated_at from pgbase_meta.file_storage_migrations order by created_at desc,id desc limit 1`).Scan(&v.ID, &v.Status, &v.CopiedObjects, &v.TotalObjects, &v.Failure, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return v, err
}

func (s *ObjectMigrationService) snapshots(ctx context.Context, destination StorageUpdate) ([]byte, []byte, error) {
	current, encrypted, err := s.controller.load(ctx)
	if err != nil {
		return nil, nil, err
	}
	secret, err := s.controller.decrypt(encrypted)
	if err != nil {
		return nil, nil, err
	}
	source := storageSnapshot{Driver: current.Driver, LocalDirectory: s.controller.localDirectory, Bucket: current.Bucket, Region: current.Region, Endpoint: current.Endpoint, AccessKey: current.AccessKey, SecretKey: string(secret), UsePathStyle: current.UsePathStyle, AllowInsecureLoopback: s.controller.allowInsecureLoopback}
	destinationSecret := string(secret)
	if destination.ClearSecret {
		destinationSecret = ""
	} else if destination.SecretKey != "" {
		destinationSecret = destination.SecretKey
	}
	target := storageSnapshot{Driver: destination.Driver, LocalDirectory: s.controller.localDirectory, Bucket: destination.Bucket, Region: destination.Region, Endpoint: destination.Endpoint, AccessKey: destination.AccessKey, SecretKey: destinationSecret, UsePathStyle: destination.UsePathStyle, AllowInsecureLoopback: s.controller.allowInsecureLoopback}
	settings := StorageSettings{Driver: target.Driver, Bucket: target.Bucket, Region: target.Region, Endpoint: target.Endpoint, AccessKey: target.AccessKey, UsePathStyle: target.UsePathStyle, SecretConfigured: target.SecretKey != "", Revision: current.Revision}
	if err = s.controller.validate(settings); err != nil || location(current) == location(settings) {
		return nil, nil, ErrInvalid
	}
	store, err := buildSnapshot(ctx, target)
	if err != nil {
		return nil, nil, ErrInvalid
	}
	if target.Driver == "s3" {
		if err = probeStore(ctx, store); err != nil {
			return nil, nil, err
		}
	}
	encode := func(value storageSnapshot) ([]byte, error) {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return s.controller.encrypt(raw)
	}
	a, err := encode(source)
	if err != nil {
		return nil, nil, err
	}
	b, err := encode(target)
	return a, b, err
}

func buildSnapshot(ctx context.Context, snapshot storageSnapshot) (ObjectStore, error) {
	if snapshot.Driver == "local" {
		return NewLocalStore(snapshot.LocalDirectory)
	}
	return NewS3Store(ctx, S3Config{Bucket: snapshot.Bucket, Region: snapshot.Region, Endpoint: snapshot.Endpoint, AccessKey: snapshot.AccessKey, SecretKey: snapshot.SecretKey, UsePathStyle: snapshot.UsePathStyle})
}

func (s *ObjectMigrationService) decodeSnapshot(blob []byte) (storageSnapshot, error) {
	raw, err := s.controller.decrypt(blob)
	var value storageSnapshot
	if err == nil {
		err = json.Unmarshal(raw, &value)
	}
	return value, err
}

func (s *ObjectMigrationService) run(ctx context.Context, id uuid.UUID) error {
	for {
		status, err := s.Status(ctx, id)
		if err != nil {
			return err
		}
		if status.Status == "canceling" {
			_, err = s.controller.pool.Exec(ctx, `update pgbase_meta.file_storage_migrations set status='canceled',updated_at=CURRENT_TIMESTAMP where id=$1 and status='canceling'`, id)
			return err
		}
		if status.Status != "copying" && status.Status != "ready" {
			return nil
		}
		copied, err := s.copyOne(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return s.fail(ctx, id, err)
		}
		if copied {
			continue
		}
		activated, err := s.activate(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return s.fail(ctx, id, err)
		}
		if activated {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (s *ObjectMigrationService) copyOne(ctx context.Context, id uuid.UUID) (bool, error) {
	var key string
	var sourceBlob, destinationBlob []byte
	claim := uuid.New()
	err := s.controller.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `select o.object_key,m.source_config_ciphertext,m.destination_config_ciphertext from pgbase_meta.file_storage_migration_objects o join pgbase_meta.file_storage_migrations m on m.id=o.migration_id where o.migration_id=$1 and (o.status in ('pending','failed') or (o.status='copying' and o.claim_expires_at<CURRENT_TIMESTAMP)) and not m.cancel_requested order by o.object_key limit 1 for update of o skip locked`, id).Scan(&key, &sourceBlob, &destinationBlob); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `update pgbase_meta.file_storage_migration_objects set status='copying',attempts=attempts+1,claim_token=$3,claim_expires_at=CURRENT_TIMESTAMP+interval '5 minutes',updated_at=CURRENT_TIMESTAMP where migration_id=$1 and object_key=$2`, id, key, claim)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var referenced bool
	if err = s.controller.pool.QueryRow(ctx, `select exists(select 1 from pgbase_meta.record_files where status in ('pending','ready') and (object_key=$1 or thumbnail_object_key=$1) union all select 1 from pgbase_meta.record_file_variants where object_key=$1 union all select 1 from pgbase_meta.extension_objects where object_key=$1)`, key).Scan(&referenced); err != nil {
		return false, err
	}
	if !referenced {
		_, err = s.controller.pool.Exec(ctx, `with changed as (update pgbase_meta.file_storage_migration_objects set status='verified',failure='',claim_token=null,claim_expires_at=null,updated_at=CURRENT_TIMESTAMP where migration_id=$1 and object_key=$2 and claim_token=$3 returning 1) update pgbase_meta.file_storage_migrations set copied_objects=copied_objects+(select count(*) from changed),updated_at=CURRENT_TIMESTAMP where id=$1`, id, key, claim)
		return true, err
	}
	heartbeatDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatDone:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = s.controller.pool.Exec(ctx, `update pgbase_meta.file_storage_migration_objects set claim_expires_at=CURRENT_TIMESTAMP+interval '5 minutes' where migration_id=$1 and object_key=$2 and claim_token=$3 and status='copying'`, id, key, claim)
			}
		}
	}()
	defer close(heartbeatDone)
	sourceConfig, err := s.decodeSnapshot(sourceBlob)
	if err != nil {
		return false, err
	}
	destinationConfig, err := s.decodeSnapshot(destinationBlob)
	if err != nil {
		return false, err
	}
	source, err := buildSnapshot(ctx, sourceConfig)
	if err != nil {
		return false, err
	}
	destination, err := buildSnapshot(ctx, destinationConfig)
	if err != nil {
		return false, err
	}
	size, digest, created, err := copyVerified(ctx, source, destination, key)
	if err != nil {
		_, _ = s.controller.pool.Exec(ctx, `update pgbase_meta.file_storage_migration_objects set status='failed',failure='copy or verification failed',claim_token=null,claim_expires_at=null,updated_at=CURRENT_TIMESTAMP where migration_id=$1 and object_key=$2 and claim_token=$3`, id, key, claim)
		return false, err
	}
	var canceled bool
	if checkErr := s.controller.pool.QueryRow(ctx, `select cancel_requested from pgbase_meta.file_storage_migrations where id=$1`, id).Scan(&canceled); checkErr != nil {
		return false, checkErr
	}
	if canceled {
		if created {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			_ = destination.Delete(cleanup, key)
		}
		_, err = s.controller.pool.Exec(ctx, `update pgbase_meta.file_storage_migration_objects set status='failed',failure='migration canceled',claim_token=null,claim_expires_at=null,updated_at=CURRENT_TIMESTAMP where migration_id=$1 and object_key=$2 and claim_token=$3`, id, key, claim)
		return true, err
	}
	_, err = s.controller.pool.Exec(ctx, `with changed as (update pgbase_meta.file_storage_migration_objects set status='verified',size_bytes=$3,sha256=$4,created_destination=$6,failure='',claim_token=null,claim_expires_at=null,updated_at=CURRENT_TIMESTAMP where migration_id=$1 and object_key=$2 and status='copying' and claim_token=$5 and exists(select 1 from pgbase_meta.file_storage_migrations where id=$1 and not cancel_requested) returning 1) update pgbase_meta.file_storage_migrations set copied_objects=copied_objects+(select count(*) from changed),updated_at=CURRENT_TIMESTAMP where id=$1`, id, key, size, digest, claim, created)
	if err != nil {
		return false, err
	}
	var finalStatus string
	if err = s.controller.pool.QueryRow(ctx, `select status from pgbase_meta.file_storage_migration_objects where migration_id=$1 and object_key=$2`, id, key).Scan(&finalStatus); err != nil {
		return false, err
	}
	if finalStatus != "verified" && created {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = destination.Delete(cleanup, key)
	}
	return true, nil
}

func copyVerified(ctx context.Context, source, destination ObjectStore, key string) (int64, []byte, bool, error) {
	in, err := source.Open(ctx, key)
	if err != nil {
		return 0, nil, false, err
	}
	defer func() { _ = in.Close() }()
	tmp, err := os.CreateTemp("", "pgbase-storage-migration-*")
	if err != nil {
		return 0, nil, false, err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(&contextReader{ctx: ctx, reader: in}, maximumMigrationObjectBytes+1))
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, nil, false, err
	}
	if size > maximumMigrationObjectBytes {
		return 0, nil, false, ErrInvalid
	}
	digest := hash.Sum(nil)
	existing, existingErr := destination.Open(ctx, key)
	if existingErr == nil {
		existingHash := sha256.New()
		existingSize, readErr := io.Copy(existingHash, io.LimitReader(&contextReader{ctx: ctx, reader: existing}, maximumMigrationObjectBytes+1))
		closeErr := existing.Close()
		if readErr != nil {
			return 0, nil, false, readErr
		}
		if closeErr != nil {
			return 0, nil, false, closeErr
		}
		if existingSize == size && equalBytes(existingHash.Sum(nil), digest) {
			return size, digest, false, nil
		}
		return 0, nil, false, ErrStorageConflict
	}
	if !errors.Is(existingErr, ErrNotFound) {
		return 0, nil, false, existingErr
	}
	tmp, err = os.Open(name)
	if err != nil {
		return 0, nil, false, err
	}
	err = destination.Put(ctx, key, tmp)
	_ = tmp.Close()
	if err != nil {
		return 0, nil, false, err
	}
	out, err := destination.Open(ctx, key)
	if err != nil {
		return 0, nil, false, err
	}
	verified := sha256.New()
	verifiedSize, readErr := io.Copy(verified, io.LimitReader(&contextReader{ctx: ctx, reader: out}, maximumMigrationObjectBytes+1))
	closeErr := out.Close()
	if readErr != nil {
		err = readErr
	} else if closeErr != nil {
		err = closeErr
	}
	if err != nil || verifiedSize != size || !equalBytes(digest, verified.Sum(nil)) {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = destination.Delete(cleanup, key)
		if err == nil {
			err = errors.New("copied object digest mismatch")
		}
		return 0, nil, false, err
	}
	return size, digest, true, nil
}

const maximumMigrationObjectBytes int64 = 8 << 30

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var different byte
	for i := range a {
		different |= a[i] ^ b[i]
	}
	return different == 0
}

func (s *ObjectMigrationService) activate(ctx context.Context, id uuid.UUID) (bool, error) {
	activated := false
	err := s.controller.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, storageTransitionLock); err != nil {
			return err
		}
		var base int64
		var sourceBlob, destinationBlob []byte
		var canceled bool
		if err := tx.QueryRow(ctx, `select base_revision,source_config_ciphertext,destination_config_ciphertext,cancel_requested from pgbase_meta.file_storage_migrations where id=$1 for update`, id).Scan(&base, &sourceBlob, &destinationBlob, &canceled); err != nil {
			return err
		}
		if canceled {
			_, err := tx.Exec(ctx, `update pgbase_meta.file_storage_migrations set status='canceled',updated_at=CURRENT_TIMESTAMP where id=$1`, id)
			return err
		}
		if err := insertMigrationObjects(ctx, tx, id); err != nil {
			return err
		}
		var pending int
		if err := tx.QueryRow(ctx, `select count(*) from pgbase_meta.file_storage_migration_objects where migration_id=$1 and status<>'verified'`, id).Scan(&pending); err != nil {
			return err
		}
		if pending != 0 {
			_, err := tx.Exec(ctx, `update pgbase_meta.file_storage_migrations set status='copying',total_objects=(select count(*) from pgbase_meta.file_storage_migration_objects where migration_id=$1),updated_at=CURRENT_TIMESTAMP where id=$1`, id)
			return err
		}
		target, err := s.decodeSnapshot(destinationBlob)
		if err != nil {
			return err
		}
		var secret []byte
		if target.SecretKey != "" {
			secret, err = s.controller.encrypt([]byte(target.SecretKey))
			if err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx, `update pgbase_meta.file_storage_settings set driver=$1,s3_bucket=$2,s3_region=$3,s3_endpoint=$4,s3_access_key=$5,s3_secret_ciphertext=$6,s3_use_path_style=$7,revision=revision+1,updated_at=CURRENT_TIMESTAMP where singleton and revision=$8`, target.Driver, target.Bucket, target.Region, target.Endpoint, target.AccessKey, secret, target.UsePathStyle, base)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrStorageConflict
		}
		if _, err = tx.Exec(ctx, `insert into pgbase_meta.file_storage_source_cleanup(migration_id,object_key,source_config_ciphertext,not_before) select $1,object_key,$2,CURRENT_TIMESTAMP+interval '1 hour' from pgbase_meta.file_storage_migration_objects where migration_id=$1 and status='verified' on conflict do nothing`, id, sourceBlob); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `update pgbase_meta.file_storage_migrations set status='committed',committed_at=CURRENT_TIMESTAMP,updated_at=CURRENT_TIMESTAMP where id=$1`, id)
		activated = err == nil
		return err
	})
	if activated {
		s.controller.mu.Lock()
		s.controller.revision = 0
		s.controller.mu.Unlock()
	}
	return activated, err
}

func (s *ObjectMigrationService) fail(ctx context.Context, id uuid.UUID, cause error) error {
	_, _ = s.controller.pool.Exec(ctx, `update pgbase_meta.file_storage_migrations set status='failed',failure=$2,updated_at=CURRENT_TIMESTAMP where id=$1 and status not in ('committed','canceled')`, id, "object migration failed")
	return fmt.Errorf("storage migration: %w", cause)
}

// CleanupOne removes one object from a retired source after the safety delay.
// Failures remain durable and are retried by later maintenance runs.
func (s *ObjectMigrationService) CleanupOne(ctx context.Context) (bool, error) {
	// Avoid placing an exclusive transition request in PostgreSQL's advisory
	// lock queue every maintenance tick when there is no cleanup work. A due
	// row can appear after this hint; the next tick will claim it, while the
	// locked query below remains authoritative for concurrent cleaners.
	var due bool
	if err := s.controller.pool.QueryRow(ctx, `select exists(select 1 from pgbase_meta.file_storage_source_cleanup where completed_at is null and not_before<=CURRENT_TIMESTAMP)`).Scan(&due); err != nil || !due {
		return false, err
	}
	worked := false
	err := s.controller.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, storageTransitionLock); err != nil {
			return err
		}
		var migrationID uuid.UUID
		var key string
		var sourceBlob []byte
		if err := tx.QueryRow(ctx, `select migration_id,object_key,source_config_ciphertext from pgbase_meta.file_storage_source_cleanup where completed_at is null and not_before<=CURRENT_TIMESTAMP order by not_before,object_key limit 1 for update skip locked`).Scan(&migrationID, &key, &sourceBlob); err != nil {
			return err
		}
		sourceSnapshot, err := s.decodeSnapshot(sourceBlob)
		if err != nil {
			return err
		}
		current, currentSecret, err := s.controller.load(ctx)
		if err != nil {
			return err
		}
		plainSecret, err := s.controller.decrypt(currentSecret)
		if err != nil {
			return err
		}
		active := storageSnapshot{Driver: current.Driver, LocalDirectory: s.controller.localDirectory, Bucket: current.Bucket, Region: current.Region, Endpoint: current.Endpoint, AccessKey: current.AccessKey, SecretKey: string(plainSecret), UsePathStyle: current.UsePathStyle, AllowInsecureLoopback: s.controller.allowInsecureLoopback}
		if snapshotLocation(sourceSnapshot) == snapshotLocation(active) {
			_, err = tx.Exec(ctx, `update pgbase_meta.file_storage_source_cleanup set not_before=CURRENT_TIMESTAMP+interval '1 hour',failure='source namespace is active' where migration_id=$1 and object_key=$2`, migrationID, key)
			return err
		}
		rows, err := tx.Query(ctx, `select source_config_ciphertext,destination_config_ciphertext from pgbase_meta.file_storage_migrations where status in ('copying','canceling','ready','activating')`)
		if err != nil {
			return err
		}
		migrationUsesSource := false
		for rows.Next() {
			var sourceConfig, destinationConfig []byte
			if err = rows.Scan(&sourceConfig, &destinationConfig); err != nil {
				return err
			}
			from, decodeErr := s.decodeSnapshot(sourceConfig)
			if decodeErr != nil {
				return decodeErr
			}
			to, decodeErr := s.decodeSnapshot(destinationConfig)
			if decodeErr != nil {
				return decodeErr
			}
			if snapshotLocation(sourceSnapshot) == snapshotLocation(from) || snapshotLocation(sourceSnapshot) == snapshotLocation(to) {
				migrationUsesSource = true
				break
			}
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		if migrationUsesSource {
			_, err = tx.Exec(ctx, `update pgbase_meta.file_storage_source_cleanup set not_before=CURRENT_TIMESTAMP+interval '1 hour',failure='source namespace is used by an active migration' where migration_id=$1 and object_key=$2`, migrationID, key)
			return err
		}
		source, err := buildSnapshot(ctx, sourceSnapshot)
		if err != nil {
			return err
		}
		deleteCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		err = source.Delete(deleteCtx, key)
		if err != nil && !errors.Is(err, ErrNotFound) {
			_, recordErr := tx.Exec(ctx, `update pgbase_meta.file_storage_source_cleanup set attempts=attempts+1,failure='source cleanup failed',not_before=CURRENT_TIMESTAMP+interval '15 minutes' where migration_id=$1 and object_key=$2`, migrationID, key)
			return errors.Join(err, recordErr)
		}
		_, err = tx.Exec(ctx, `update pgbase_meta.file_storage_source_cleanup set completed_at=CURRENT_TIMESTAMP,failure='' where migration_id=$1 and object_key=$2`, migrationID, key)
		worked = err == nil
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return worked, err
}

func snapshotLocation(s storageSnapshot) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%t", s.Driver, s.LocalDirectory, s.Bucket, s.Region, s.Endpoint, s.UsePathStyle)
}

func insertMigrationObjects(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if _, err := tx.Exec(ctx, `insert into pgbase_meta.file_storage_migration_objects(migration_id,object_key,object_kind)
select $1,object_key,kind from (
 select object_key,'original' kind from pgbase_meta.record_files where status in ('pending','ready')
 union select thumbnail_object_key,'thumbnail' from pgbase_meta.record_files where status='ready' and thumbnail_object_key is not null
 union select object_key,'variant' from pgbase_meta.record_file_variants
 union select object_key,'extension' from pgbase_meta.extension_objects
) objects where object_key<>'' on conflict do nothing`, id); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `insert into pgbase_meta.file_storage_migration_objects(migration_id,object_key,object_kind,status) select $1,object_key,'cleanup','verified' from pgbase_meta.file_object_deletions where object_key<>'' on conflict do nothing`, id)
	return err
}

const referencedObjectCountSQL = `select count(*) from (select object_key from pgbase_meta.record_files where status in ('pending','ready') union select thumbnail_object_key from pgbase_meta.record_files where status='ready' and thumbnail_object_key is not null union select object_key from pgbase_meta.record_file_variants union select object_key from pgbase_meta.extension_objects union select object_key from pgbase_meta.file_object_deletions) objects where object_key<>''`
