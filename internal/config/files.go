package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/webisnoteasy/PgBase/pgbase"
)

func loadFiles(lookup func(string) (string, bool), options *pgbase.Options) error {
	driver, _ := value(lookup, "PGBASE_FILES_DRIVER")
	files := pgbase.FileOptions{Driver: driver}
	files.LocalDirectory, _ = value(lookup, "PGBASE_FILES_LOCAL_DIRECTORY")
	files.S3Bucket, _ = value(lookup, "PGBASE_FILES_S3_BUCKET")
	files.S3Region, _ = value(lookup, "PGBASE_FILES_S3_REGION")
	files.S3Endpoint, _ = value(lookup, "PGBASE_FILES_S3_ENDPOINT")
	files.S3AccessKey, _ = value(lookup, "PGBASE_FILES_S3_ACCESS_KEY")
	files.S3SecretKey, _ = value(lookup, "PGBASE_FILES_S3_SECRET_KEY")
	if err := fileBool(lookup, "PGBASE_FILES_S3_PATH_STYLE", &files.S3UsePathStyle); err != nil {
		return err
	}
	if err := fileBool(lookup, "PGBASE_FILES_S3_ALLOW_INSECURE_LOOPBACK", &files.S3AllowInsecureLoopback); err != nil {
		return err
	}
	if err := fileInt64(lookup, "PGBASE_FILES_MAX_BYTES", &files.MaximumBytes); err != nil {
		return err
	}
	if err := fileInt64(lookup, "PGBASE_FILES_MAX_IMAGE_PIXELS", &files.MaximumImagePixels); err != nil {
		return err
	}
	var thumbnail int64
	if err := fileInt64(lookup, "PGBASE_FILES_THUMBNAIL_DIMENSION", &thumbnail); err != nil {
		return err
	}
	files.ThumbnailDimension = int(thumbnail)
	if raw, ok := value(lookup, "PGBASE_FILES_DOWNLOAD_TOKEN_TTL"); ok && raw != "" {
		duration, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("PGBASE_FILES_DOWNLOAD_TOKEN_TTL: %w", err)
		}
		files.DownloadTokenTTL = duration
	}
	if raw, ok := value(lookup, "PGBASE_FILES_ALLOWED_TYPES"); ok && raw != "" {
		for _, item := range strings.Split(raw, ",") {
			contentType := strings.TrimSpace(item)
			if contentType == "" {
				return fmt.Errorf("PGBASE_FILES_ALLOWED_TYPES: contains an empty type")
			}
			files.AllowedTypes = append(files.AllowedTypes, contentType)
		}
	}
	options.Files = files
	return nil
}

func fileBool(lookup func(string) (string, bool), name string, target *bool) error {
	raw, ok := value(lookup, name)
	if !ok || raw == "" {
		return nil
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	*target = parsed
	return nil
}

func fileInt64(lookup func(string) (string, bool), name string, target *int64) error {
	raw, ok := value(lookup, name)
	if !ok || raw == "" {
		return nil
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed < 1 {
		return fmt.Errorf("%s: must be a positive integer", name)
	}
	*target = parsed
	return nil
}
