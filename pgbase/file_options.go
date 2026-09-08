package pgbase

import (
	"fmt"
	"time"
)

type FileOptions struct {
	Driver             string
	LocalDirectory     string
	S3Bucket           string
	S3Region           string
	S3Endpoint         string
	S3AccessKey        string
	S3SecretKey        string
	S3UsePathStyle     bool
	MaximumBytes       int64
	MaximumImagePixels int64
	ThumbnailDimension int
	DownloadTokenTTL   time.Duration
	AllowedTypes       []string
}

func (o FileOptions) normalized() (FileOptions, error) {
	if o.Driver == "" {
		if o.LocalDirectory != "" || o.S3Bucket != "" || o.S3Region != "" || o.S3Endpoint != "" || o.S3AccessKey != "" || o.S3SecretKey != "" || o.S3UsePathStyle || o.MaximumBytes != 0 || o.MaximumImagePixels != 0 || o.ThumbnailDimension != 0 || o.DownloadTokenTTL != 0 || len(o.AllowedTypes) != 0 {
			return FileOptions{}, fmt.Errorf("Files.Driver: required when Files options are set")
		}
		return o, nil
	}
	if o.MaximumBytes == 0 {
		o.MaximumBytes = 10 << 20
	}
	if o.MaximumImagePixels == 0 {
		o.MaximumImagePixels = 40_000_000
	}
	if o.ThumbnailDimension == 0 {
		o.ThumbnailDimension = 320
	}
	if o.DownloadTokenTTL == 0 {
		o.DownloadTokenTTL = 5 * time.Minute
	}
	if len(o.AllowedTypes) == 0 {
		o.AllowedTypes = []string{"image/jpeg", "image/png", "application/pdf", "text/plain"}
	}
	if o.MaximumBytes < 1 || o.MaximumBytes > 100<<20 {
		return FileOptions{}, fmt.Errorf("Files.MaximumBytes: must be between 1 byte and 100 MiB")
	}
	if o.MaximumImagePixels < 1 || o.MaximumImagePixels > 100_000_000 || o.ThumbnailDimension < 1 || o.ThumbnailDimension > 2048 || o.DownloadTokenTTL <= 0 || o.DownloadTokenTTL > 15*time.Minute {
		return FileOptions{}, fmt.Errorf("Files: invalid image or download-token bound")
	}
	switch o.Driver {
	case "local":
		if o.LocalDirectory == "" {
			return FileOptions{}, fmt.Errorf("Files.LocalDirectory: is required for local storage")
		}
	case "s3":
		if o.S3Bucket == "" || o.S3Region == "" || (o.S3AccessKey == "") != (o.S3SecretKey == "") {
			return FileOptions{}, fmt.Errorf("Files: S3 bucket, region, and complete optional credentials are required")
		}
	default:
		return FileOptions{}, fmt.Errorf("Files.Driver: must be local or s3")
	}
	o.AllowedTypes = append([]string(nil), o.AllowedTypes...)
	return o, nil
}
