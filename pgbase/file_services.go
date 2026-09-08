package pgbase

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/api"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/filestore"
	"github.com/webisnoteasy/PgBase/internal/identity"
	"github.com/webisnoteasy/PgBase/internal/recordaccess"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

func withFileServices(ctx context.Context, dependencies api.Dependencies, pool *database.Pool, options Options, schemas *schema.Service) (api.Dependencies, *filestore.Service, error) {
	if options.Files.Driver == "" {
		return dependencies, nil, nil
	}
	var objects filestore.ObjectStore
	var err error
	switch options.Files.Driver {
	case "local":
		objects, err = filestore.NewLocalStore(options.Files.LocalDirectory)
	case "s3":
		objects, err = filestore.NewS3Store(ctx, filestore.S3Config{Bucket: options.Files.S3Bucket, Region: options.Files.S3Region, Endpoint: options.Files.S3Endpoint, AccessKey: options.Files.S3AccessKey, SecretKey: options.Files.S3SecretKey, UsePathStyle: options.Files.S3UsePathStyle})
	}
	if err != nil {
		return api.Dependencies{}, nil, err
	}
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
	service, err := filestore.NewService(pool, objects, authorize, filestore.Options{MaximumBytes: options.Files.MaximumBytes, MaximumImagePixels: options.Files.MaximumImagePixels, ThumbnailDimension: options.Files.ThumbnailDimension, AllowedTypes: options.Files.AllowedTypes, TokenSecret: options.TokenSecret, TokenTTL: options.Files.DownloadTokenTTL})
	if err != nil {
		return api.Dependencies{}, nil, fmt.Errorf("configure file service: %w", err)
	}
	dependencies.Files = service
	return dependencies, service, nil
}
