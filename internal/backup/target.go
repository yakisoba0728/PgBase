package backup

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Target is a newly restored database and local object tree. Its connection URL
// is secret configuration: persist privately and never include it in API jobs.
type Target struct {
	ownership      *targetOwnership
	DatabaseURL    string `json:"databaseUrl"`
	FilesDirectory string `json:"filesDirectory"`
}

// RestoreNew restores to a uniquely named database on the configured server.
// The original database and object store are never overwritten. A restored S3
// inventory becomes an isolated local tree, which also permits offline recovery.
func RestoreNew(ctx context.Context, directory, databaseURL, targetRoot string) (target Target, resultErr error) {
	if _, err := VerifyContext(ctx, directory); err != nil {
		return Target{}, err
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return Target{}, err
	}
	name := "pgbase_restore_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	conn, err := connectDatabase(ctx, databaseURL)
	if err != nil {
		return Target{}, err
	}
	_, err = conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE template0")
	closeErr := conn.Close(context.Background())
	if err != nil {
		return Target{}, err
	}
	complete := false
	ownedFiles := ""
	defer func() {
		if complete {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		db, err := connectDatabase(cleanup, databaseURL)
		if err == nil {
			_, err = db.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
			err = errors.Join(err, db.Close(cleanup))
		}
		resultErr = errors.Join(resultErr, err)
		if ownedFiles != "" {
			resultErr = errors.Join(resultErr, os.RemoveAll(ownedFiles))
		}
	}()
	if closeErr != nil {
		return Target{}, closeErr
	}
	parsed.Path = "/" + name
	parsed.RawPath = ""
	absolute, err := filepath.Abs(targetRoot)
	if err != nil {
		return Target{}, err
	}
	target = Target{DatabaseURL: parsed.String(), FilesDirectory: filepath.Join(absolute, name, "files"), ownership: &targetOwnership{controlURL: databaseURL, name: name, directory: filepath.Join(absolute, name)}}
	ownedFiles = filepath.Dir(target.FilesDirectory)
	if err = os.MkdirAll(filepath.Dir(target.FilesDirectory), 0700); err != nil {
		return Target{}, err
	}
	if err = Progress(ctx, "restoring_database_and_files"); err != nil {
		return Target{}, err
	}
	if err = Restore(ctx, directory, target.DatabaseURL, target.FilesDirectory); err != nil {
		return Target{}, err
	}
	complete = true
	return target, nil
}
