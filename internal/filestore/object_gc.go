package filestore

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

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
		rows, err := tx.Query(ctx, `select object_key from pgbase_meta.file_object_deletions order by queued_at,object_key limit 100`)
		if err != nil {
			return err
		}
		keys := make([]string, 0, 100)
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				rows.Close()
				return err
			}
			keys = append(keys, key)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, key := range keys {
			var referenceLock, stagingLock bool
			if err := tx.QueryRow(ctx, `select pg_try_advisory_xact_lock(hashtextextended($1,0))`, "file-object/"+key).Scan(&referenceLock); err != nil {
				return err
			}
			if !referenceLock {
				continue
			}
			if err := tx.QueryRow(ctx, `select pg_try_advisory_xact_lock(hashtextextended($1,0))`, stagedObjectLockName(key)).Scan(&stagingLock); err != nil {
				return err
			}
			if !stagingLock {
				continue
			}
			var selected string
			err := tx.QueryRow(ctx, `select object_key from pgbase_meta.file_object_deletions where object_key=$1 for update skip locked`, key).Scan(&selected)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			var referenced bool
			if err := tx.QueryRow(ctx, `select exists(select 1 from pgbase_meta.record_files where status in ('pending','ready') and (object_key=$1 or thumbnail_object_key=$1) union all select 1 from pgbase_meta.record_file_variants where object_key=$1 union all select 1 from pgbase_meta.extension_objects where object_key=$1)`, key).Scan(&referenced); err != nil {
				return err
			}
			if !referenced {
				if err := s.objects.Delete(ctx, key); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `delete from pgbase_meta.file_object_deletions where object_key=$1`, key); err != nil {
				return err
			}
			worked = true
			return nil
		}
		return nil
	})
	return worked, err
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
