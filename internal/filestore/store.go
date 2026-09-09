// Package filestore stores private record attachments behind the same
// authorization decision as their owning record.
package filestore

import (
	"context"
	"errors"
	"io"
	"regexp"
)

var (
	ErrInvalid  = errors.New("invalid file")
	ErrTooLarge = errors.New("file too large")
	ErrNotFound = errors.New("file not found")
	ErrDenied   = errors.New("file access denied")
	ErrTooMany  = errors.New("too many files")
	ErrRequired = errors.New("file is required")
)

type ObjectStore interface {
	Put(context.Context, string, io.Reader) error
	Open(context.Context, string) (io.ReadCloser, error)
	Delete(context.Context, string) error
}

// ObjectGCKey gates physical object deletion against an exclusive backup
// inventory/copy session.
const ObjectGCKey int64 = 0x5047424146494c45

var storedObjectKey = regexp.MustCompile(`^(?:objects|thumbnails)/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
