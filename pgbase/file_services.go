package pgbase

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/api"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/recordops"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type requiredFileValidator struct{ schemas *schema.Service }

func (v requiredFileValidator) ValidateRecords(ctx context.Context, tx pgx.Tx, request recordops.BatchRequest, results []recordops.MutationResult) error {
	catalog, err := v.schemas.EnsureCurrent(ctx, tx)
	if err != nil {
		return err
	}
	checked := map[string]bool{}
	required := map[uuid.UUID]bool{}
	for _, collection := range catalog.Collections {
		for _, field := range collection.Fields {
			if field.Type == schema.TypeFile && field.Required {
				required[collection.ID] = true
				break
			}
		}
	}
	for index, result := range results {
		if result.Record == nil || index >= len(request.Operations) {
			continue
		}
		operation := request.Operations[index]
		collectionName := recordOperationCollection(operation)
		if collectionName == "" {
			continue
		}
		collection, ok := catalog.Collections[collectionName]
		rawID, idOK := result.Record.Data["id"].(string)
		recordID, parseErr := uuid.Parse(rawID)
		if !ok || !idOK || parseErr != nil {
			return schema.ErrConflict
		}
		if !required[collection.ID] {
			continue
		}
		key := collection.ID.String() + "/" + recordID.String()
		if checked[key] {
			continue
		}
		checked[key] = true
		var missing string
		err = tx.QueryRow(ctx, `select coalesce((select f.name from pgbase_meta.fields f
			where f.collection_id=$1 and f.field_type='file' and f.required
			and exists(select 1 from pgbase_meta.record_revisions r where r.collection_id=$1 and r.record_id=$2)
			and not exists(select 1 from pgbase_meta.record_files rf where rf.collection_id=$1 and rf.record_id=$2 and rf.field_name=f.name and rf.status='ready')
			order by f.ordinal limit 1),'')`, collection.ID, recordID).Scan(&missing)
		if err != nil {
			return err
		}
		if missing != "" {
			return &query.ValidationError{Details: []query.ValidationDetail{{Field: missing, Code: "required_file"}}}
		}
	}
	return nil
}

func recordOperationCollection(operation recordops.BatchOperation) string {
	switch {
	case operation.Create != nil:
		return operation.Create.Collection
	case operation.Update != nil:
		return operation.Update.Collection
	case operation.Upsert != nil:
		return operation.Upsert.Collection
	default:
		return ""
	}
}

func withFileServices(ctx context.Context, dependencies api.Dependencies, pool *database.Pool, options Options, schemas *schema.Service) (api.Dependencies, *filestore.Service, filestore.ObjectStore, error) {
	if options.Files.Driver == "" {
		return dependencies, nil, nil, nil
	}
	objects, err := filestore.NewStorageController(ctx, pool, options.TokenSecret, filestore.StorageConfig{Driver: options.Files.Driver, LocalDirectory: options.Files.LocalDirectory, Bucket: options.Files.S3Bucket, Region: options.Files.S3Region, Endpoint: options.Files.S3Endpoint, AccessKey: options.Files.S3AccessKey, SecretKey: options.Files.S3SecretKey, UsePathStyle: options.Files.S3UsePathStyle, AllowInsecureLoopback: options.Files.S3AllowInsecureLoopback})
	if err != nil {
		return api.Dependencies{}, nil, nil, err
	}
	dependencies.StorageAdministration = objects
	if options.Hooks != nil {
		dependencies.StorageAdministration = hookedStorageAdministration{service: objects, registry: options.Hooks}
	}
	dependencies.StorageMigrations = filestore.NewObjectMigrationService(objects)
	authorize := func(ctx context.Context, tx pgx.Tx, principal identity.Subject, collectionID, recordID uuid.UUID, field string, operation filestore.Operation) error {
		catalog, err := schemas.EnsureCurrent(ctx, tx)
		if err != nil {
			return err
		}
		var collection schema.Collection
		for _, candidate := range catalog.Collections {
			if candidate.ID == collectionID {
				collection = candidate
				break
			}
		}
		if collection.ID == uuid.Nil {
			return filestore.ErrNotFound
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM pgbase_meta.collections c
			JOIN pgbase_meta.fields f ON f.collection_id=c.id
			JOIN pgbase_meta.record_revisions r ON r.collection_id=c.id
			WHERE c.id=$1 AND r.record_id=$2 AND f.name=$3 AND f.field_type='file')`, collectionID, recordID, field).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return filestore.ErrNotFound
		}
		if principal.Kind == identity.Administrator {
			return nil
		}
		if principal.Kind != identity.User {
			return filestore.ErrDenied
		}
		if err := recordaccess.AuthorizeUser(ctx, tx, collectionID, principal.ID); err != nil {
			if err == recordaccess.ErrDenied || err == recordaccess.ErrUnsupported {
				return filestore.ErrDenied
			}
			return err
		}
		action := "view"
		if operation == filestore.OperationWrite {
			action = "update"
		}
		allowed, err := recordaccess.AllowsRecord(ctx, tx, collection, principal, action, recordID, false)
		if err != nil {
			return err
		}
		if !allowed {
			return filestore.ErrNotFound
		}
		return nil
	}
	policy := func(ctx context.Context, tx pgx.Tx, collectionID uuid.UUID, fieldName string) (filestore.FieldPolicy, error) {
		catalog, err := schemas.EnsureCurrent(ctx, tx)
		if err != nil {
			return filestore.FieldPolicy{}, err
		}
		for _, collection := range catalog.Collections {
			if collection.ID != collectionID {
				continue
			}
			for _, field := range collection.Fields {
				if field.Name != fieldName || field.Type != schema.TypeFile {
					continue
				}
				protected := field.Options.Protected == nil || *field.Options.Protected
				result := filestore.FieldPolicy{MaximumBytes: 5 << 20, MaximumCount: 1, MimeTypes: append([]string(nil), options.Files.AllowedTypes...), Thumbs: append([]string(nil), field.Options.Thumbs...), Required: field.Required, Hidden: field.Hidden, Protected: &protected}
				if field.Options.MaxFileSize != nil {
					result.MaximumBytes = *field.Options.MaxFileSize
				}
				if field.Options.MaxSelect != nil {
					result.MaximumCount = *field.Options.MaxSelect
				}
				if len(field.Options.MimeTypes) != 0 {
					result.MimeTypes = append([]string(nil), field.Options.MimeTypes...)
				}
				return result, nil
			}
		}
		return filestore.FieldPolicy{}, filestore.ErrNotFound
	}
	service, err := filestore.NewService(pool, objects, authorize, filestore.Options{MaximumBytes: options.Files.MaximumBytes, MaximumImagePixels: options.Files.MaximumImagePixels, ThumbnailDimension: options.Files.ThumbnailDimension, AllowedTypes: options.Files.AllowedTypes, TokenSecret: options.TokenSecret, TokenTTL: options.Files.DownloadTokenTTL, Policy: policy, Hooks: options.Hooks})
	if err != nil {
		return api.Dependencies{}, nil, nil, fmt.Errorf("configure file service: %w", err)
	}
	dependencies.Files = service
	return dependencies, service, objects, nil
}
