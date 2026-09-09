package config

import (
	"fmt"
	"strconv"

	"github.com/webisnoteasy/PgBase/pgbase"
)

func loadBackups(lookup func(string) (string, bool), options *pgbase.BackupOptions) error {
	options.Directory, _ = value(lookup, "PGBASE_BACKUPS_DIRECTORY")
	options.S3.Bucket, _ = value(lookup, "PGBASE_BACKUPS_S3_BUCKET")
	options.S3.Prefix, _ = value(lookup, "PGBASE_BACKUPS_S3_PREFIX")
	options.S3.Region, _ = value(lookup, "PGBASE_BACKUPS_S3_REGION")
	options.S3.Endpoint, _ = value(lookup, "PGBASE_BACKUPS_S3_ENDPOINT")
	options.S3.AccessKey, _ = value(lookup, "PGBASE_BACKUPS_S3_ACCESS_KEY")
	options.S3.SecretKey, _ = value(lookup, "PGBASE_BACKUPS_S3_SECRET_KEY")
	if raw, ok := value(lookup, "PGBASE_BACKUPS_RETENTION"); ok && raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			return fmt.Errorf("PGBASE_BACKUPS_RETENTION: must be a non-negative integer")
		}
		options.Retention = parsed
	}
	if raw, ok := value(lookup, "PGBASE_BACKUPS_S3_PATH_STYLE"); ok && raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("PGBASE_BACKUPS_S3_PATH_STYLE: %w", err)
		}
		options.S3.UsePathStyle = parsed
	}
	return nil
}
