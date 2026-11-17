package filestore

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
)

// ResetRestoredStorage detaches a newly restored database from every object
// namespace owned by its source installation. The restore boundary has already
// unpacked the consistent file snapshot into its configured local directory;
// no persisted S3 credential or deferred worker claim may survive that cut.
func ResetRestoredStorage(ctx context.Context, pool *database.Pool) error {
	if pool == nil {
		return ErrInvalid
	}
	return pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, storageTransitionLock); err != nil {
			return err
		}
		// Pending rows describe uploads that were not committed into the backup
		// snapshot. Their trigger may queue source keys, so clear every inherited
		// deletion after removing them.
		if _, err := tx.Exec(ctx, `delete from pgbase_meta.record_files where status='pending'`); err != nil {
			return err
		}
		// This isolated restore owns a new local namespace, not the source's
		// still-running remote writes. Clearing their retention here must not
		// cause a restored worker to act on source storage credentials.
		// Managed restore runs this boundary before the restored application
		// applies migrations. Backups from schema 68 have no retention column.
		var hasRetention bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from pg_attribute where attrelid='pgbase_meta.file_object_deletions'::regclass and attname='retain_on_success' and not attisdropped)`).Scan(&hasRetention); err != nil {
			return err
		}
		if hasRetention {
			if _, err := tx.Exec(ctx, `update pgbase_meta.file_object_deletions set retain_on_success=false`); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `delete from pgbase_meta.file_object_deletions`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `delete from pgbase_meta.file_storage_migrations`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `insert into pgbase_meta.file_storage_settings(singleton,driver)
			values(true,'local') on conflict(singleton) do update set
			driver='local',s3_bucket='',s3_region='',s3_endpoint='',s3_access_key='',
			s3_secret_ciphertext=null,s3_use_path_style=false,
			revision=pgbase_meta.file_storage_settings.revision+1,updated_at=CURRENT_TIMESTAMP`)
		return err
	})
}
