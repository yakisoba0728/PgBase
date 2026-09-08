// Package filestore stores private record attachments behind the same
// authorization decision as their owning record.
package filestore

import (
	"context"
	"errors"
	"io"
)

var (
	ErrInvalid  = errors.New("invalid file")
	ErrTooLarge = errors.New("file too large")
	ErrNotFound = errors.New("file not found")
	ErrDenied   = errors.New("file access denied")
)

type ObjectStore interface {
	Put(context.Context, string, io.Reader) error
	Open(context.Context, string) (io.ReadCloser, error)
	Delete(context.Context, string) error
}
