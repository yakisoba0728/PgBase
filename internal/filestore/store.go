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

var storedObjectKey = regexp.MustCompile(`^(?:(?:objects|thumbnails)/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|extensions/[0-9a-f]{64}/[0-9a-f]{64}(?:/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})?)$`)

// ObjectPage is a bounded page returned by stores that can inventory object
// keys. Callers must treat keys as opaque storage keys.
type ObjectPage struct {
	Keys       []string
	NextCursor string
}

// ObjectLister is optional so existing object-store implementations remain
// compatible. Extension namespaces use their database inventory for listing.
type ObjectLister interface {
	List(context.Context, string, string, int) (ObjectPage, error)
}

// ObjectCopier is optional. Generic callers can stream through Open and Put
// when a backend does not support a native copy operation.
type ObjectCopier interface {
	Copy(context.Context, string, string) error
}

// SizedObjectStore accepts a known byte count for transports, such as S3,
// that require Content-Length for non-chunked uploads.
type SizedObjectStore interface {
	PutSized(context.Context, string, io.Reader, int64) error
}
