package filestore

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
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
