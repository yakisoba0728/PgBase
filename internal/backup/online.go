package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
)

// ObjectGCKey is shared with the file store. Every physical deletion holds the
// shared lock; an online snapshot holds the exclusive lock until its immutable
// object inventory has been copied. Logical record writes continue meanwhile.
const ObjectGCKey int64 = 0x5047424146494c45
const schemaSnapshotKey int64 = 0x7067626173650002

type ObjectSource interface {
	OpenObject(context.Context, string) (io.ReadCloser, error)
}

// CreateOnline exports one repeatable-read snapshot to pg_dump and reads the
// record and extension object inventory from that exact snapshot. It never
// includes uncommitted or pending uploads. Holding the GC gate makes concurrently
// deleted objects remain readable until the copy is complete, for local and S3
// stores alike.
func CreateOnline(ctx context.Context, destination, databaseURL string, objects ObjectSource) (resultErr error) {
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return errors.New("backup directory already exists")
	}
	if err := Progress(ctx, "waiting_for_snapshot"); err != nil {
		return err
	}
	command, err := commandEnvironment(databaseURL)
	if err != nil {
		return err
	}
	defer command.cleanup()
	conn, err := connectDatabase(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, conn.Close(context.Background())) }()
	// Take session locks before beginning the snapshot: a GC that was already
	// deleting may commit while we wait. Snapshotting first could retain its rows
	// after their objects have disappeared.
	if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock($1),pg_advisory_lock_shared($2)`, ObjectGCKey, schemaSnapshotKey); err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var snapshot string
	var version int
	if err = tx.QueryRow(ctx, `SELECT pg_export_snapshot(),current_setting('server_version_num')::int`).Scan(&snapshot, &version); err != nil {
		return err
	}
	major, err := toolMajor(ctx, "pg_dump")
	if err != nil {
		return err
	}
	if major != version/10000 {
		return fmt.Errorf("pg_dump major %d does not match PostgreSQL major %d", major, version/10000)
	}
	keys, err := snapshotObjectKeys(ctx, tx)
	if err != nil {
		return err
	}
	if len(keys) > 0 && objects == nil {
		return errors.New("file storage is required to back up referenced objects")
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(filepath.Dir(destination), ".online-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(temporary) }()
	if err = os.Mkdir(filepath.Join(temporary, "files"), 0700); err != nil {
		return err
	}
	if err = Progress(ctx, "dumping_database"); err != nil {
		return err
	}
	dump := filepath.Join(temporary, "database.dump")
	if err = run(ctx, "pg_dump", []string{"--format=custom", "--no-owner", "--no-acl", "--schema=pgbase_meta", "--schema=app", "--schema=pgbase_relation", "--snapshot", snapshot, "--file", dump}, command.env); err != nil {
		return err
	}
	manifest := Manifest{Format: FormatVersion, CreatedAt: time.Now().UTC(), PostgreSQLMajor: major, Files: []File{}}
	manifest.Database, err = checksum(ctx, dump, "database.dump")
	if err != nil {
		return err
	}
	for index, key := range keys {
		if index%100 == 0 {
			if err = Progress(ctx, fmt.Sprintf("copying_objects_%d_of_%d", index, len(keys))); err != nil {
				return err
			}
		}
		relative, err := cleanRelative(key)
		if err != nil {
			return err
		}
		path := filepath.Join(temporary, "files", relative)
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		source, err := objects.OpenObject(ctx, key)
		if err != nil {
			return fmt.Errorf("read referenced backup object: %w", err)
		}
		out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			_ = source.Close()
			return err
		}
		_, copyErr := io.Copy(out, contextReader{ctx, source})
		err = errors.Join(copyErr, out.Close(), source.Close())
		if err != nil {
			return err
		}
		item, err := checksum(ctx, path, key)
		if err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, item)
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(temporary, "manifest.json"), append(encoded, '\n'), 0600); err != nil {
		return err
	}
	if _, err = VerifyContext(ctx, temporary); err != nil {
		return err
	}
	// The exported snapshot's connection must still be alive before publication.
	if err = tx.QueryRow(ctx, "SELECT 1").Scan(&version); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return os.Rename(temporary, destination)
}

func snapshotObjectKeys(ctx context.Context, tx pgx.Tx) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT object_key FROM pgbase_meta.record_files WHERE status = 'ready'
 UNION SELECT thumbnail_object_key FROM pgbase_meta.record_files WHERE status = 'ready' AND thumbnail_object_key IS NOT NULL
 UNION SELECT v.object_key FROM pgbase_meta.record_file_variants v JOIN pgbase_meta.record_files f ON f.id=v.file_id WHERE f.status = 'ready'
 UNION SELECT object_key FROM pgbase_meta.extension_objects ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}
