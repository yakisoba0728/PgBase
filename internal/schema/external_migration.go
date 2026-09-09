package schema

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ValidateExternalMigration compiles catalog metadata in the caller's write
// transaction. A user migration cannot commit malformed PgBase metadata.

// ExternalMigrationManifest validates catalog metadata and returns the physical
// contract that must still hold before the caller commits its transaction.
func ExternalMigrationManifest(ctx context.Context, tx pgx.Tx) (ExpectedPhysicalSchema, error) {
	if tx == nil {
		return ExpectedPhysicalSchema{}, fmt.Errorf("validate external migration: transaction is nil")
	}
	store := NewStore(tx)
	version, err := store.LockedSchemaVersion(ctx)
	if err != nil {
		return ExpectedPhysicalSchema{}, err
	}
	catalog, err := store.ReadCatalog(ctx, version)
	if err != nil {
		return ExpectedPhysicalSchema{}, classifyCatalogPreflightError(err)
	}
	if _, err = compileCatalogWithLimit(catalog, maximumCatalogWireBytes); err != nil {
		return ExpectedPhysicalSchema{}, classifyCatalogPreflightError(err)
	}
	manifest, err := ExpectedPhysicalSchemaForCatalog(catalog)
	return manifest, classifyCatalogPreflightError(err)
}

// ValidateExternalMigration retains the metadata-only validation seam for
// callers that cannot inspect PostgreSQL physical objects.
func ValidateExternalMigration(ctx context.Context, tx pgx.Tx) error {
	_, err := ExternalMigrationManifest(ctx, tx)
	return err
}
